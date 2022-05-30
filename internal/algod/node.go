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
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/algonode/algovnode/internal/blockfetcher"
	"github.com/algonode/algovnode/internal/config"
	"github.com/algonode/algovnode/internal/utils"
	"github.com/algorand/go-algorand-sdk/client/v2/algod"
	"github.com/algorand/go-algorand-sdk/client/v2/common"
	"github.com/algorand/go-algorand-sdk/client/v2/common/models"
	"github.com/algorand/go-algorand/protocol"
	"github.com/algorand/go-algorand/rpcs"
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
	cluster     *NodeCluster
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
	//	node.log.Debugf("lastRound is %d", node.latestRound)
	node.Unlock()
	return true
}

func (node *Node) UpdateStatus(ctx context.Context) bool {
	var nodeStatus *models.NodeStatus
	err := utils.Backoff(ctx, func(actx context.Context) (fatal bool, err error) {
		ns, err := node.algodClient.Status().Do(actx)
		if err != nil {
			node.log.WithError(err).Warn("GetStatus")
			return false, err
		}
		nodeStatus = &ns
		return false, nil
	}, time.Second*10, time.Millisecond*100, time.Second*10, 0)
	if err != nil {
		//Ctx got cancelled, exit silently
		return false
	}
	if node.UpdateWithStatus(nodeStatus) {
		node.cluster.SetLatestRound(node.latestRound, node)
		return true
	}
	return false
}

func (node *Node) Boot(ctx context.Context) bool {
	node.SetState(AnsBooting, "Booting")
	node.latestRound = 0

	var genesis string = ""
	err := utils.Backoff(ctx, func(actx context.Context) (fatal bool, err error) {
		g, err := node.algodClient.GetGenesis().Do(actx)
		if err != nil {
			node.log.WithError(err).Warn("GetGenesis")
			return false, err
		}
		genesis = g
		return false, nil
	}, time.Second*10, time.Millisecond*100, time.Second*10, 0)
	if err != nil {
		//Ctx got cancelled, exit silently
		return false
	}

	//Make sure all nodes have the same genesis
	if err := node.cluster.EnsureGenesis(genesis); err != nil {
		//Singal fatal error
		node.log.WithError(err).Error()
		node.cluster.fatalErr <- err
		return false
	}
	node.genesis = genesis

	if node.FetchBlockRaw(ctx, 0) {
		node.log.Infof("Node is archival")
		node.catchup = false
	}

	return node.UpdateStatus(ctx)
}
func (node *Node) UpdateTTL(us int64) {
	node.Lock()
	if node.ttlEwma > 99_999 {
		node.ttlEwma = float32(us) / 1000.0
	} else {
		node.ttlEwma = node.ttlEwma*0.9 + float32(us)*0.1/1000.0
	}
	node.Unlock()
}

func (node *Node) UpdateStatusAfter(ctx context.Context) uint64 {
	var nodeStatus *models.NodeStatus
	var lr uint64 = 0
	err := utils.Backoff(ctx, func(actx context.Context) (fatal bool, err error) {
		//skip ahead
		lr = node.cluster.GetLatestRound()
		ns, err := node.algodClient.StatusAfterBlock(lr).Do(actx)
		if err != nil {
			node.log.WithError(err).Warnf("StatusAfterBlock %d", lr)
			return false, err
		}
		nodeStatus = &ns
		return false, nil
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

func isFatalAPIError(err error) bool {
	if _, ok := err.(common.BadRequest); ok {
		return true
	}
	if _, ok := err.(common.InternalError); ok {
		return true
	}
	if _, ok := err.(common.InvalidToken); ok {
		return true
	}
	if _, ok := err.(common.NotFound); ok {
		return true
	}
	return false
}

func (node *Node) FetchBlockRaw(ctx context.Context, bn uint64) bool {
	block := new(rpcs.EncodedBlockCert)
	var rawBlock []byte
	node.log.Infof("Fetching block %d", bn)
	err := utils.Backoff(ctx, func(actx context.Context) (fatal bool, err error) {
		start := time.Now()
		rb, err := node.algodClient.BlockRaw(bn).Do(ctx)

		if err != nil {
			if isFatalAPIError(err) {
				node.UpdateTTL(time.Since(start).Microseconds())
				return true, err
			}
			node.log.WithError(err).Warnf("BlockRaw %d", bn)
			return false, err
		}
		node.UpdateTTL(time.Since(start).Microseconds())

		err = protocol.Decode(rb, block)
		if err != nil {
			node.log.WithError(err).Warn()
			return false, err
		}

		rawBlock = rb
		return false, nil
	}, time.Second*10, time.Millisecond*100, time.Second*10, 10)
	if err != nil {
		node.log.WithError(err).Errorf("BlockRaw %d", bn)
		return false
	}
	node.BlockSink(block, rawBlock)
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
		clr := node.cluster.GetLatestRound()
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

func (gs *NodeCluster) AddNode(ctx context.Context, cfg *config.ANode) error {
	node := &Node{
		state:    AnsConfig,
		state_at: time.Now(),
		catchup:  true,
		ttlEwma:  100_000,
		cluster:  gs,
		cfg:      cfg,
		genesis:  "",
	}
	node.log = logrus.WithFields(logrus.Fields{"nodeId": cfg.Id, "state": &node.state, "ttlMs": &node.ttlEwma})
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

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func (node *Node) BlockSink(block *rpcs.EncodedBlockCert, blockRaw []byte) bool {
	if node.cluster.SetLatestRound(uint64(block.Block.BlockHeader.Round), node) {
		//we won the race
		bw, err := blockfetcher.MakeBlockWrap(node.cfg.Id, block, blockRaw)
		if err != nil {
			node.log.WithError(err).Errorf("Error making blockWrap")
			return false
		}
		if node.cluster.ucache != nil {
			node.cluster.ucache.Sink <- bw
			node.log.Infof("Block %d is now lastest, sd:%d", block.Block.BlockHeader.Round, len(node.cluster.ucache.Sink))
		} else {
			node.log.Errorf("Block %d discarded, no sink", block.Block.BlockHeader.Round)
		}
		return true
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
