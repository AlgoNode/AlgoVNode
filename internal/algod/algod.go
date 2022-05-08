// Copyright (C) 2022 AlgoNode Org.
//
// algonode is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as
// published by the Free Software Foundation, either version 3 of the
// License, or (at your option) any later version.
//
// algonode is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with algonode.  If not, see <https://www.gnu.org/licenses/>.

package algod

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/algonode/algovnode/internal/config"
	"github.com/algonode/algovnode/internal/utils"
	"github.com/algorand/go-algorand-sdk/client/v2/algod"
	"github.com/algorand/go-algorand-sdk/client/v2/common"
	"github.com/algorand/go-algorand-sdk/client/v2/common/models"
	"github.com/algorand/go-algorand-sdk/encoding/msgpack"
	"github.com/algorand/go-algorand-sdk/types"
	"github.com/sirupsen/logrus"
)

type ANState int64

const (
	AnsConfig ANState = iota
	AnsBooting
	AnsSyncing
	AnsSynced
	AnsFailed
)

func (s ANState) String() string {
	switch s {
	case AnsBooting:
		return "Booting"
	case AnsSyncing:
		return "Syncing"
	case AnsSynced:
		return "Synced"
	case AnsConfig:
		return "Config"
	case AnsFailed:
		return "Failed"
	default:
		return fmt.Sprintf("%d", int(s))
	}
}

type Node struct {
	sync.Mutex
	cfg         *config.ANode
	log         *logrus.Entry
	httpClient  *http.Client
	algodClient *algod.Client
	genesis     string
	catchup     bool
	ttlEwma     float32
	latestRound uint64
	state       ANState
	state_at    time.Time
	gState      *GlobalState
}

type GlobalState struct {
	sync.Mutex
	fatalErr    chan error
	blockSink   chan *BlockWrap
	latestRound uint64
	lastSrc     *Node
	lastAt      time.Time
	genesis     string
	nodes       []*Node
	//TODO lruCache
}

func (gs *GlobalState) GetLatestRound() uint64 {
	gs.Lock()
	lr := gs.latestRound
	gs.Unlock()
	return lr
}

func (gs *GlobalState) SetLatestRound(lr uint64, node *Node) bool {
	gs.Lock()
	defer gs.Unlock()
	if lr > gs.latestRound {
		gs.latestRound = lr
		gs.lastAt = time.Now()
		gs.lastSrc = node
		return true
	}
	return false
}

func (gs *GlobalState) EnsureGenesis(g string) error {
	gs.Lock()
	defer gs.Unlock()
	if gs.genesis == "" {
		gs.genesis = g
		return nil
	}
	if gs.genesis == g {
		return nil
	}
	return errors.New("Genesis mismatch")
}

func (gs *GlobalState) HandleFatal(ctx context.Context) {
	select {
	case <-ctx.Done():
	case err := <-gs.fatalErr:
		logrus.WithError(err).Error("Quitting due to fatal error")
	}
}

func (node *Node) UpdateWithStatus(nodeStatus *models.NodeStatus) bool {
	if nodeStatus.StoppedAtUnsupportedRound {
		node.SetState(AnsFailed, "Upgrade required")
		return false
	}

	if nodeStatus.CatchupTime > 0 {
		node.SetState(AnsSyncing, "Not synced")
	} else {
		node.SetState(AnsSynced, "Synced")
	}
	node.Lock()
	if nodeStatus.LastRound > node.latestRound {
		node.latestRound = nodeStatus.LastRound
	}
	node.log.Debugf("lastRound is %d", node.latestRound)
	node.Unlock()
	return true
}

func (node *Node) UpdateStatus(ctx context.Context) bool {
	var nodeStatus *models.NodeStatus
	err := utils.Backoff(ctx, func(actx context.Context) error {
		start := time.Now()
		ns, err := node.algodClient.Status().Do(actx)
		if err != nil {
			node.log.WithError(err).Warn("GetStatus")
			return err
		}
		dur := time.Since(start).Milliseconds()
		node.ttlEwma = node.ttlEwma*0.9 + float32(dur)*0.1
		nodeStatus = &ns
		return nil
	}, time.Second*10, time.Millisecond*100, time.Second*10, 0)
	if err != nil {
		//Ctx got cancelled, exit silently
		return false
	}
	if node.UpdateWithStatus(nodeStatus) {
		node.gState.SetLatestRound(node.latestRound, node)
		return true
	}
	return false
}

func (node *Node) Boot(ctx context.Context) bool {
	node.SetState(AnsBooting, "Booting")
	node.latestRound = 0

	var genesis string = ""
	err := utils.Backoff(ctx, func(actx context.Context) error {
		g, err := node.algodClient.GetGenesis().Do(actx)
		if err != nil {
			node.log.WithError(err).Warn("GetGenesis")
			return err
		}
		genesis = g
		return nil
	}, time.Second*10, time.Millisecond*100, time.Second*10, 0)
	if err != nil {
		//Ctx got cancelled, exit silently
		return false
	}

	//Make sure all nodes have the same genesis
	if err := node.gState.EnsureGenesis(genesis); err != nil {
		//Singal fatal error
		node.log.WithError(err).Error()
		node.gState.fatalErr <- err
		return false
	}
	node.genesis = genesis
	return node.UpdateStatus(ctx)
}
func (node *Node) UpdateTTL(ms int64) {
	node.Lock()
	node.ttlEwma = node.ttlEwma*0.9 + float32(ms)*0.1
	node.Unlock()
}

func (node *Node) UpdateStatusAfter(ctx context.Context) uint64 {
	var nodeStatus *models.NodeStatus
	var lr uint64 = 0
	err := utils.Backoff(ctx, func(actx context.Context) error {
		//skip ahead
		lr = node.gState.GetLatestRound()
		start := time.Now()
		ns, err := node.algodClient.StatusAfterBlock(lr).Do(actx)
		if err != nil {
			node.log.WithError(err).Warnf("StatusAfterBlock %d", lr)
			return err
		}
		node.UpdateTTL(time.Since(start).Milliseconds())
		nodeStatus = &ns
		return nil
	}, time.Second*10, time.Millisecond*100, time.Second*10, 10)
	if err != nil {
		node.log.WithError(err).Errorf("StatusAfterBlock %d", lr)
		//reboot
		return 0
	}
	if !node.UpdateWithStatus(nodeStatus) {
		//reboot
		return 0
	}
	return lr
}

func (node *Node) FetchBlockRaw(ctx context.Context, bn uint64) bool {
	var block *types.Block
	var rawBlock []byte

	err := utils.Backoff(ctx, func(actx context.Context) error {

		rb, err := node.algodClient.BlockRaw(bn).Do(ctx)
		if err != nil {
			node.log.WithError(err).Warnf("BlockRaw %d", bn)
			return err
		}
		var response models.BlockResponse
		msgpack.CodecHandle.ErrorIfNoField = false
		if err = msgpack.Decode(rb, &response); err != nil {
			node.log.WithError(err).Warn()
			return err
		}
		block = &response.Block
		rawBlock = rb
		return nil
	}, time.Second*10, time.Millisecond*100, time.Second*10, 10)
	if err != nil {
		node.log.WithError(err).Errorf("BlockRaw %d", bn)
		//reboot
		return false
	}
	if node.BlockSink(block, rawBlock) {
		node.log.Infof("Block %d is now lastest", block.Round)
	}
	return true
}

func (node *Node) Monitor(ctx context.Context) {
	for ctx.Err() == nil {
		//TODO - detect node swap mainnet -> testnet

		lr := node.UpdateStatusAfter(ctx)
		if lr == 0 {
			//reboot
			node.log.Errorf("Rebooting node due to issue with UpdateStatusAfter")
			break
		}

		if node.state != AnsSynced {
			node.log.Warn("Node is syncing, skipping block fetch")
			time.Sleep(time.Second)
			continue
		}
		clr := node.gState.GetLatestRound()
		if clr < lr+1 {
			if !node.FetchBlockRaw(ctx, lr+1) {
				//reboot
				node.log.Errorf("Rebooting node due to issue with Fetch Block")
				break
			}
		} else {
			node.log.Debugf("Skipping already fetched block %d", clr)
		}
	}
}

func (node *Node) MainLoop(ctx context.Context) {
	for ctx.Err() == nil {
		if !node.Boot(ctx) {
			time.Sleep(time.Second)
			continue
		}
		node.Monitor(ctx)
		time.Sleep(time.Second)
	}
}

func (gs *GlobalState) AddNode(ctx context.Context, cfg *config.ANode) error {
	node := &Node{
		state:    AnsConfig,
		state_at: time.Now(),
		catchup:  true,
		ttlEwma:  300,
		gState:   gs,
		cfg:      cfg,
		genesis:  "",
	}
	node.log = logrus.WithFields(logrus.Fields{"nodeId": cfg.Id, "state": &node.state})
	gs.nodes = append(gs.nodes, node)
	return node.Start(ctx)
}

func (node *Node) Start(ctx context.Context) error {
	hdrs := []*common.Header{
		{Key: "Referer", Value: "http://AlgoNode.VN1"},
	}
	aClient, err := algod.MakeClientWithHeaders(node.cfg.Address, node.cfg.Token, hdrs)
	if err != nil {
		node.log.WithError(err).Error()
		return err
	}
	node.algodClient = aClient

	ht := http.DefaultTransport.(*http.Transport).Clone()
	ht.MaxIdleConns = 50
	ht.MaxConnsPerHost = 50
	ht.MaxIdleConnsPerHost = 50

	node.httpClient = &http.Client{
		Timeout:   10 * time.Second,
		Transport: ht,
	}
	go node.MainLoop(ctx)
	return nil
}

type NodeStatus struct {
	LastRound uint64
	LagMs     int64
	NodeId    string
	LastCP    string
}

type BlockWrap struct {
	Bn       uint64
	Block    *types.Block
	BlockRaw []byte
	Src      *Node
	Ts       time.Time
}

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func (node *Node) BlockSink(block *types.Block, blockRaw []byte) bool {
	if node.gState.SetLatestRound(uint64(block.Round), node) {
		//we won the race
		bw := &BlockWrap{
			Bn:       uint64(block.Round),
			Ts:       time.Now(),
			Block:    block,
			BlockRaw: blockRaw,
			Src:      node,
		}
		node.gState.blockSink <- bw
		return true
	}
	return false
}

func proxyStatus(proxyStatuses *[]int, status int) bool {
	if proxyStatuses == nil {
		return true
	}
	for _, s := range *proxyStatuses {
		if s == status {
			return true
		}
	}
	return false
}

func (node *Node) SetState(state ANState, reason string) {
	node.Lock()
	defer node.Unlock()
	if state == node.state {
		return
	}
	oldState := node.state
	oldStateAt := node.state_at
	node.state = state
	node.state_at = time.Now()
	node.log.WithFields(logrus.Fields{"oldState": oldState.String(), "durationSec": math.Round(node.state_at.Sub(oldStateAt).Seconds()), "reason": reason}).Info("State change")
}

func (node *Node) ProxyHTTP(wr http.ResponseWriter, req *http.Request, proxyStatuses *[]int) (bool, int, error) {
	node.log.WithFields(logrus.Fields{"method": req.Method, "url": req.URL}).Info()
	req.RequestURI = ""
	//Todo: keep-alive ??
	//ratelimiter
	//parallel calls to all catchup
	//constant scanning on all catchup
	//direct query relay nodes for block ranges
	//parallel catchup queries
	resp, err := node.httpClient.Do(req)
	if err != nil {
		http.Error(wr, "Server Error", http.StatusInternalServerError)
		node.log.WithFields(logrus.Fields{"ServeHTTP:": err}).Error()
		return false, http.StatusInternalServerError, err
	}
	defer resp.Body.Close()
	if proxyStatus(proxyStatuses, resp.StatusCode) {
		copyHeader(wr.Header(), resp.Header)
		wr.WriteHeader(resp.StatusCode)
		io.Copy(wr, resp.Body)
		return true, resp.StatusCode, nil
	}
	return false, resp.StatusCode, nil
}

//globalMaxBlock holds the highest read block across all connected nodes
//writes must use atomic interface
//reads are safe as the var is 64bit aligned

/*
var globalMaxBlock uint64 = 0

func AlgoVNode(ctx context.Context, acfg *AConfig) (chan *BlockWrap, chan *Status, error) {
	qDepth := acfg.Buffer
	if qDepth < 1 {
		qDepth = 100
	}
	bestbchan := make(chan *BlockWrap, qDepth)
	bchan := make(chan *BlockWrap, qDepth)
	schan := make(chan *Status, qDepth)

	for idx := range acfg.ANodes {
		if err := algodStreamNode(ctx, acfg, idx, bchan, schan, acfg.FRound, acfg.LRound); err != nil {
			return nil, nil, err
		}
	}

	// filter duplicates, forward only first newer blocks.
	go func() {
		var maxBlock uint64 = math.MaxUint64
		var maxTs time.Time = time.Now()
		var maxLeader string = "'"
		for {
			select {
			case bw := <-bchan:
				if uint64(bw.Block.Round) > maxBlock || maxBlock == math.MaxUint64 {
					bestbchan <- bw
					maxBlock = uint64(bw.Block.Round)
					atomic.StoreUint64(&globalMaxBlock, maxBlock)
					maxTs = bw.Ts
					maxLeader = bw.Src
				} else {
					if maxBlock == uint64(bw.Block.Round) {
						fmt.Fprintf(os.Stderr, "[INFO][ALGOD] Block from %s is %v behind %s\n", bw.Src, bw.Ts.Sub(maxTs), maxLeader)
					}
				}
			case <-ctx.Done():
			}
		}
	}()

	return bestbchan, schan, nil
}

func algodStreamNode(ctx context.Context, acfg *AConfig, idx int, bchan chan *BlockWrap, schan chan *Status, start int64, stop int64) error {

	cfg := acfg.ANodes[idx]
	// Create an algod client
	algodClient, err := algod.MakeClient(cfg.Address, cfg.Token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[!ERR][ALGOD][%s] failed to make algod client: %s\n", cfg.Id, err)
		return err
	}
	fmt.Fprintf(os.Stderr, "[INFO][ALGOD][%s] new algod client: %s\n", cfg.Id, cfg.Address)

	//Loop until Algoverse gets cancelled
	go func() {

		var nodeStatus *models.NodeStatus = nil
		utils.Backoff(ctx, func(actx context.Context) error {
			ns, err := algodClient.Status().Do(actx)
			if err != nil {
				return fmt.Errorf("[!ERR][ALGOD][%s] %s\n", cfg.Id, err.Error())
			}
			nodeStatus = &ns
			return nil
		}, time.Second*10, time.Millisecond*100, time.Second*10)
		if nodeStatus == nil {
			fmt.Fprintf(os.Stderr, "[!ERR][ALGOD][%s] Unable to start node\n", cfg.Id)
			return
		}
		schan <- &Status{NodeId: cfg.Id, LastCP: nodeStatus.LastCatchpoint, LastRound: uint64(nodeStatus.LastRound), LagMs: int64(nodeStatus.TimeSinceLastRound) / int64(time.Millisecond)}

		var nextRound uint64 = 0
		if start < 0 {
			nextRound = nodeStatus.LastRound
			fmt.Fprintf(os.Stderr, "[WARN][ALGOD][%s] Starting from last round : %d\n", cfg.Id, nodeStatus.LastRound)
		} else {
			nextRound = uint64(start)
			fmt.Fprintf(os.Stderr, "[WARN][ALGOD][%s] Starting from fixed round : %d\n", cfg.Id, nextRound)
		}

		ustop := uint64(stop)
		for stop < 0 || nextRound <= ustop {
			for ; nextRound <= nodeStatus.LastRound; nextRound++ {
				err := utils.Backoff(ctx, func(actx context.Context) error {
					gMax := globalMaxBlock
					//skip old blocks in case other nodes are ahead of us
					if gMax > nextRound {
						fmt.Fprintf(os.Stderr, "[WARN][ALGOD][%s] skipping ahead %d blocks to %d\n", cfg.Id, gMax-nextRound, gMax)
						nextRound = globalMaxBlock
					}
					rawBlock, err := algodClient.BlockRaw(nextRound).Do(ctx)
					if err != nil {
						return fmt.Errorf("[!ERR][ALGOD][%s] %s", cfg.Id, err.Error())
					}
					var response models.BlockResponse
					msgpack.CodecHandle.ErrorIfNoField = false
					if err = msgpack.Decode(rawBlock, &response); err != nil {
						return fmt.Errorf("[!ERR][ALGOD][%s] %s", cfg.Id, err.Error())
					}
					block := response.Block

					//fmt.Fprintf(os.Stderr, "got block %d, queue %d\n", block.Round, len(bchan))
					select {
					case bchan <- &BlockWrap{
						Block:    &block,
						BlockRaw: rawBlock,
						Ts:       time.Now(),
						Src:      cfg.Id,
					}:
					case <-ctx.Done():
					}
					return ctx.Err()
				}, time.Second*10, time.Millisecond*100, time.Second*10)
				if err != nil || nextRound >= ustop {
					return
				}
			}

			err := utils.Backoff(ctx, func(actx context.Context) error {
				newStatus, err := algodClient.StatusAfterBlock(nodeStatus.LastRound).Do(actx)
				if err != nil {
					return fmt.Errorf("[!ERR][ALGOD][%s] %s", cfg.Id, err.Error())
				}
				nodeStatus = &newStatus
				//fmt.Fprintf(os.Stderr, "algod last round: %d, lag: %s\n", nodeStatus.LastRound, time.Duration(nodeStatus.TimeSinceLastRound)*time.Nanosecond)
				schan <- &Status{NodeId: cfg.Id, LastRound: uint64(nodeStatus.LastRound), LagMs: int64(nodeStatus.TimeSinceLastRound) / int64(time.Millisecond)}
				return nil
			}, time.Second*10, time.Millisecond*100, time.Second*10)

			if err != nil {
				return
			}

		}
	}()

	return nil
}
*/

func Main(ctx context.Context, cfg config.AlgoVNodeConfig) {
	gState := GlobalState{
		genesis:     "",
		latestRound: 0,
		fatalErr:    make(chan error),
		blockSink:   make(chan *BlockWrap, 1000),
		nodes:       make([]*Node, 0),
	}
	for _, n := range cfg.Algod.Nodes {
		gState.AddNode(ctx, n)
	}
	//TODO sink
	gState.HandleFatal(ctx)
}
