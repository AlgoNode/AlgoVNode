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

package node

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/algonode/algovnode/internal/config"
	"github.com/algonode/algovnode/internal/icluster"
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
	AnsFailing
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
	case AnsFailing:
		return "Failing"
	case AnsFailed:
		return "Failed"
	default:
		return fmt.Sprintf("%d", int(s))
	}
}

type T_RttEwma float32

func (s *T_RttEwma) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("\"%.1f\"", *s)), nil
}

func (s *T_RttEwma) String() string {
	return fmt.Sprintf("%.1f", *s)
}

func (s *ANState) MarshalJSON() ([]byte, error) {
	return []byte("\"" + s.String() + "\""), nil
}

type Node struct {
	sync.RWMutex
	Catchup     bool
	lastStatus  *models.NodeStatus
	cfg         *config.NodeCfg
	log         *logrus.Entry
	httpClient  *http.Client
	algodClient *algod.Client
	genesis     string
	rttEwma     float32
	latestRound uint64
	state       ANState
	state_at    time.Time
	cluster     icluster.Cluster
}

func (node *Node) LastStatus() *models.NodeStatus {
	node.RLock()
	defer node.RUnlock()
	return node.lastStatus
}

func (node *Node) Synced() bool {
	node.RLock()
	defer node.RUnlock()
	return node.state == AnsSynced
}

func (node *Node) updateWithStatus(nodeStatus *models.NodeStatus) bool {
	if nodeStatus.StoppedAtUnsupportedRound {
		node.setState(AnsFailed, "Upgrade required")
		return false
	}

	if nodeStatus.CatchupTime > 0 {
		node.setState(AnsSyncing, "Not synced")
	} else {
		node.setState(AnsSynced, "Synced")
	}
	node.Lock()
	if nodeStatus.LastRound > node.latestRound {
		node.latestRound = nodeStatus.LastRound
	}
	node.lastStatus = nodeStatus
	//	node.log.Debugf("lastRound is %d", node.latestRound)
	node.Unlock()
	return true
}

func (node *Node) updateStatus(ctx context.Context) bool {
	var nodeStatus *models.NodeStatus
	err := utils.Backoff(ctx, func(actx context.Context) (fatal bool, err error) {
		start := time.Now()
		ns, err := node.algodClient.Status().Do(actx)
		if err != nil {
			node.log.WithError(err).Warn("GetStatus")
			node.setState(AnsFailing, err.Error())
			return false, err
		}
		node.updateRTT(time.Since(start).Microseconds())
		nodeStatus = &ns
		return false, nil
	}, time.Second*10, time.Millisecond*100, time.Second*10, 0)
	if err != nil {
		//Ctx got cancelled, exit silently
		return false
	}
	if node.updateWithStatus(nodeStatus) {
		node.cluster.LatestRoundSet(node.latestRound, nodeStatus)
		return true
	}
	return false
}

func (node *Node) boot(ctx context.Context) bool {
	node.setState(AnsBooting, "Booting")
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
	if err := node.cluster.GenesisEnsure(genesis); err != nil {
		//Singal fatal error
		node.log.WithError(err).Error()
		node.cluster.FatalError(err)
		return false
	}
	node.genesis = genesis

	if node.FetchBlockRaw(ctx, 0) {
		node.log.Infof("Node is archival")
		node.Catchup = false
	}

	return node.updateStatus(ctx)
}
func (node *Node) updateRTT(us int64) {
	node.Lock()
	if node.rttEwma > 99_999 {
		node.rttEwma = float32(us) / 1000.0
	} else {
		node.rttEwma = 0.9*node.rttEwma + 0.1*float32(us)/1000.0
	}
	node.Unlock()
}

func (node *Node) GetRTT() float32 {
	node.RLock()
	defer node.RUnlock()
	return node.rttEwma
}

func (node *Node) updateStatusAfter(ctx context.Context) uint64 {
	var nodeStatus *models.NodeStatus
	var lr uint64 = 0
	err := utils.Backoff(ctx, func(actx context.Context) (fatal bool, err error) {
		//skip ahead
		lr = node.cluster.LatestRoundGet()
		ns, err := node.algodClient.StatusAfterBlock(lr).Do(actx)
		if err != nil {
			if err != context.Canceled {
				node.log.WithError(err).Warn("updateStatusAfter")
				node.setState(AnsFailing, err.Error())
			}
			return false, err
		}
		nodeStatus = &ns
		return false, nil
	}, time.Second*10, time.Millisecond*100, time.Second*10, 10)
	if err != nil {
		if err != context.Canceled {
			node.log.WithError(err).Errorf("StatusAfterBlock %d", lr)
		}
		//reboot
		return 0
	}
	if !node.updateWithStatus(nodeStatus) {
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

func (node *Node) FetchBlockRaw(ctx context.Context, round uint64) bool {
	block := new(rpcs.EncodedBlockCert)
	var rawBlock []byte
	start := time.Now()
	err := utils.Backoff(ctx, func(actx context.Context) (fatal bool, err error) {
		start := time.Now()
		rb, err := node.algodClient.BlockRaw(round).Do(ctx)
		if err != nil {
			if isFatalAPIError(err) {
				return true, err
			}
			node.log.WithError(err).Warnf("BlockRaw %d", round)
			return false, err
		}
		node.updateRTT(time.Since(start).Microseconds())
		err = protocol.Decode(rb, block)
		if err != nil {
			node.log.WithError(err).Warn()
			return false, err
		}

		rawBlock = rb
		return false, nil
	}, time.Second*10, time.Millisecond*100, time.Second*10, 10)
	if err != nil {
		node.log.WithError(err).Errorf("BlockRaw %d", round)
		node.cluster.BlockSinkError(round, node.cfg.Id, err)
		return false
	}
	node.log.Debugf("Fetched block %d in %.1fms", round, 0.001*float32(time.Since(start).Microseconds()))
	node.cluster.BlockSink(round, node.cfg.Id, rawBlock)
	return true
}

func (node *Node) Monitor(ctx context.Context) {
	for ctx.Err() == nil {
		//TODO - detect node swap mainnet -> testnet

		lr := node.updateStatusAfter(ctx)
		if lr == 0 {
			//Reconnect
			if ctx.Err() == nil {
				node.log.Errorf("Reconnecting to node due to issue with UpdateStatusAfter")
			}
			break
		}

		if node.state != AnsSynced {
			node.log.Warn("Node is syncing, skipping block fetch")
			time.Sleep(time.Second)
			continue
		}
		clr := node.cluster.LatestRoundGet()
		if clr < lr+1 {
			if !node.FetchBlockRaw(ctx, lr+1) {
				//Reconnect
				if ctx.Err() == nil {
					node.log.Errorf("Reconnecting to node due to issue with Fetch Block")
				}
				break
			}
		} else {
			node.log.Tracef("Skipping already fetched block %d", clr)
		}
	}
}

func (node *Node) mainLoop(ctx context.Context) {
	for ctx.Err() == nil {
		if !node.boot(ctx) {
			time.Sleep(time.Second)
			continue
		}
		node.Monitor(ctx)
		time.Sleep(time.Second)
	}
}

func (node *Node) start(ctx context.Context) error {
	hdrs := []*common.Header{
		{Key: "Referer", Value: "http://" + NODE_TAG},
		{Key: "User-Agent", Value: NODE_TAG},
	}
	aClient, err := algod.MakeClientWithHeaders(node.cfg.Address, node.cfg.Token, hdrs)
	if err != nil {
		node.log.WithError(err).Error()
		return err
	}
	node.algodClient = aClient

	ht := http.DefaultTransport.(*http.Transport).Clone()
	ht.MaxIdleConns = 150
	ht.MaxConnsPerHost = 150
	ht.MaxIdleConnsPerHost = 150

	node.httpClient = &http.Client{
		Timeout:   10 * time.Second,
		Transport: ht,
	}
	go node.mainLoop(ctx)
	return nil
}

type NodeStatus struct {
	LastRound uint64
	LagMs     int64
	NodeId    string
	LastCP    string
}

/*

func (node *Node) BlockSinkError(round uint64, err error) {
	node.cluster.SinkError(round, err)
}

func (node *Node) BlockSink(block *rpcs.EncodedBlockCert, blockRaw []byte) bool {
	round := uint64(block.Block.BlockHeader.Round)
	cluster := node.cluster
	if cluster.SetLatestRound(round, node) {
		//we won the race
		bw, err := blockfetcher.MakeBlockWrap(node.cfg.Id, block, blockRaw)
		if err != nil {
			node.log.WithError(err).Errorf("Error making blockWrap")
			return false
		}
		if cluster.ucache != nil {
			cluster.ucache.AddBlock(bw)
			node.log.Infof("Block %d is now latest", round)
		} else {
			node.log.Errorf("Block %d discarded, no sink", round)
		}
		return true
	}
	if !cluster.isBlockCached(round) {
		bw, err := blockfetcher.MakeBlockWrap(node.cfg.Id, block, blockRaw)
		if err != nil {
			node.log.WithError(err).Errorf("Error making blockWrap")
			return false
		}
		if cluster.ucache != nil {
			node.log.Debugf("Sending block %d to cache", round)
			cluster.ucache.AddBlock(bw)
			return true
		} else {
			node.log.Errorf("Block %d discarded, no sink", round)
		}
	}
	node.log.Debugf("Block %d already in cache", round)
	return false
}

*/

func (node *Node) setState(state ANState, reason string) {
	node.Lock()
	defer node.Unlock()
	if state == node.state {
		return
	}
	oldState := node.state
	oldStateAt := node.state_at
	node.state = state
	node.state_at = time.Now()
	node.cluster.StateUpdate()
	node.log.WithFields(logrus.Fields{"oldState": oldState.String(), "durationSec": math.Round(node.state_at.Sub(oldStateAt).Seconds()), "reason": reason}).Info("State change")
}

func New(ctx context.Context, cfg *config.NodeCfg, cluster icluster.Cluster, log *logrus.Entry) (*Node, error) {
	node := &Node{
		state:    AnsConfig,
		state_at: time.Now(),
		Catchup:  true,
		rttEwma:  100_000_000,
		cluster:  cluster,
		cfg:      cfg,
		genesis:  "",
	}
	node.log = log.WithFields(logrus.Fields{"nodeId": cfg.Id, "state": &node.state, "rttMs": (*T_RttEwma)(&node.rttEwma)})

	return node, node.start(ctx)
}
