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
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/algonode/algovnode/internal/blockcache"
	"github.com/algonode/algovnode/internal/config"
	"github.com/sirupsen/logrus"
)

type NodeCluster struct {
	sync.Mutex
	fatalErr     chan error
	ucache       *blockcache.UnifiedBlockCache
	latestRound  uint64
	lastSrc      *Node
	lastAt       time.Time
	genesis      string
	nodes        []*Node
	cState       chan struct{}
	catchupNodes []*Node
	archNodes    []*Node
	log          *logrus.Entry
}

//GetLatestRound returns latest round available on the cluster
func (gs *NodeCluster) GetLatestRound() uint64 {
	gs.Lock()
	lr := gs.latestRound
	gs.Unlock()
	return lr
}

//SetLatestRound sets latest round available on the cluster
func (gs *NodeCluster) SetLatestRound(lr uint64, node *Node) bool {
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

//EnsureGenesis returns error if supplied genesis hash does not match cluster genesis
func (gs *NodeCluster) EnsureGenesis(g string) error {
	gs.Lock()
	defer gs.Unlock()
	if gs.genesis == "" {
		gs.genesis = g
		return nil
	}
	if gs.genesis == g {
		return nil
	}
	return errors.New("genesis mismatch")
}

//WaitForFatal blocks until cluster receives a fatal error on error channel
func (gs *NodeCluster) WaitForFatal(ctx context.Context) {
	select {
	case <-ctx.Done():
	case err := <-gs.fatalErr:
		gs.log.WithError(err).Error("Quitting due to fatal error")
	}
}

//GetSyncNodesByTTL returns list of synced nodes ordered by status response time
func (gs *NodeCluster) GetCatchupSyncedNodesByTTL() []*Node {
	return gs.catchupNodes
}

func (gs *NodeCluster) GetArchSyncedNodesByTTL() []*Node {
	return gs.archNodes
}

func (gs *NodeCluster) StateUpdate() {
	gs.cState <- struct{}{}
}

func (gs *NodeCluster) updateNodeLists() {
	gs.Lock()
	defer gs.Unlock()
	archNodes := make([]*Node, 0)
	catchupNodes := make([]*Node, 0)
	for _, n := range gs.nodes {
		if n.Synced() {
			if n.Catchup {
				catchupNodes = append(catchupNodes, n)
			} else {
				archNodes = append(archNodes, n)
			}
		}
	}
	sort.SliceStable(catchupNodes, func(i, j int) bool {
		return catchupNodes[i].ttlEwma < catchupNodes[j].ttlEwma
	})
	sort.SliceStable(archNodes, func(i, j int) bool {
		return archNodes[i].ttlEwma < archNodes[j].ttlEwma
	})
	gs.archNodes = archNodes
	gs.catchupNodes = catchupNodes
}

//stateChangeMonitor - goroutine that listens to node status changes in the cluster and updates synced nodes lists
func (gs *NodeCluster) stateChangeMonitor(ctx context.Context) {
	for {
		select {
		case <-gs.cState:
			gs.updateNodeLists()
			gs.log.Warnf("Cluster state updated, synced nodes: %d ", len(gs.catchupNodes))
		case <-ctx.Done():
			return
		}
	}
}

//LoadBlock does
func (gs *NodeCluster) LoadBlock(ctx context.Context, round uint64) {
	//TODO
	//handle future rounds
	//handle parallel limit
	if round > gs.latestRound {
		gs.fatalErr <- errors.New("tried to load future block")
	} else {
		if round < gs.latestRound-blockcache.CatchupSize+2 {
			fetches := 0
			for _, n := range gs.catchupNodes {
				//try all catchup in parallel
				go n.FetchBlockRaw(ctx, round)
				fetches++
			}
			if fetches == 0 {
				for _, n := range gs.archNodes {
					//try all archive in parallel
					go n.FetchBlockRaw(ctx, round)
					fetches++
				}
			}
			if fetches == 0 {
				gs.log.Errorf("all nodes unavailable to load block %d", round)
			} else {
				gs.log.Debugf("fetching block %d on %d nodes", round, fetches)
			}
		} else {
			fetches := 0
			for _, n := range gs.nodes {
				//skip for some node states
				if !n.Catchup && n.state != AnsFailed {
					//try all archive in parallel
					n.FetchBlockRaw(ctx, round)
					fetches++
				}
			}
			if fetches == 0 {
				gs.log.Errorf("All nodes unavailable to load block %d", round)
			} else {
				gs.log.Debugf("Fetching block %d on %d nodes", round, fetches)
			}
		}
	}
}

func (gs *NodeCluster) addNode(ctx context.Context, cfg *config.NodeCfg) error {
	node := &Node{
		state:    AnsConfig,
		state_at: time.Now(),
		Catchup:  true,
		ttlEwma:  100_000_000,
		cluster:  gs,
		cfg:      cfg,
		genesis:  "",
	}
	node.log = gs.log.WithFields(logrus.Fields{"nodeId": cfg.Id, "state": &node.state, "ttlMs": &node.ttlEwma})
	gs.nodes = append(gs.nodes, node)
	return node.Start(ctx)
}

//NewCluster instantiates all configured nodes and returns new node cluster object
func NewCluster(ctx context.Context, ucache *blockcache.UnifiedBlockCache, cfg config.AlgoVNodeConfig, log *logrus.Entry) *NodeCluster {
	cluster := &NodeCluster{
		genesis:      "",
		latestRound:  0,
		fatalErr:     make(chan error),
		nodes:        make([]*Node, 0),
		ucache:       ucache,
		log:          log,
		catchupNodes: make([]*Node, 0),
		archNodes:    make([]*Node, 0),
		cState:       make(chan struct{}, 100),
	}

	for _, n := range cfg.Virtual.Nodes {
		cluster.addNode(ctx, n)
	}

	go cluster.stateChangeMonitor(ctx)
	return cluster

}

func init() {
	http.DefaultTransport.(*http.Transport).MaxIdleConns = 150
	http.DefaultTransport.(*http.Transport).MaxConnsPerHost = 150
	http.DefaultTransport.(*http.Transport).MaxIdleConnsPerHost = 150
}
