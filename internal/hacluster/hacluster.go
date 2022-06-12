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

package hacluster

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/algonode/algovnode/internal/blockcache"
	"github.com/algonode/algovnode/internal/blockwrap"
	"github.com/algonode/algovnode/internal/config"
	"github.com/algonode/algovnode/internal/node"
	"github.com/algonode/algovnode/internal/utils"
	"github.com/algorand/go-algorand-sdk/client/v2/common/models"
	"github.com/dustin/go-broadcast"
	"github.com/labstack/echo/v4"
	"github.com/sirupsen/logrus"
)

const (
	ERRMSG_BLK404 = "failed to retrieve information from the ledger"
)

type NodeCluster struct {
	sync.RWMutex
	fatalErr     chan error
	ucache       *blockcache.UnifiedBlockCache
	latestRound  uint64
	latestStatus *models.NodeStatus
	lastAt       time.Time
	genesis      string
	nodes        []*node.Node
	cState       chan struct{}
	catchupNodes []*node.Node
	archNodes    []*node.Node
	log          *logrus.Entry
	broadcaster  broadcast.Broadcaster
	bpSink       chan uint64
	bps          []*BlockPrefetch
	up           bool
}

func (gs *NodeCluster) broadcastListener(ctx context.Context) {
	ch := make(chan interface{})
	gs.broadcaster.Register(ch)
	defer gs.broadcaster.Unregister(ch)
	for {
		select {
		case <-ctx.Done():
			return
		case status := <-ch:
			gs.log.Tracef("Cluster is at round %d", status.(*models.NodeStatus).LastRound)
		}
	}
}

//GetSyncNodesByRTT returns list of synced nodes ordered by status response time
func (gs *NodeCluster) getCatchupSyncedNodesByRTT() []*node.Node {
	return gs.catchupNodes
}

func (gs *NodeCluster) getArchSyncedNodesByRTT() []*node.Node {
	return gs.archNodes
}

func (gs *NodeCluster) getSyncedNodesByRTT() []*node.Node {
	catchupNodes := gs.getCatchupSyncedNodesByRTT()
	archiveNodes := gs.getArchSyncedNodesByRTT()

	nodes := make([]*node.Node, 0, len(catchupNodes)+len(archiveNodes))
	nodes = append(nodes, catchupNodes...)
	nodes = append(nodes, archiveNodes...)
	return nodes
}

func (gs *NodeCluster) updateNodeLists() {
	gs.Lock()
	defer gs.Unlock()
	archNodes := make([]*node.Node, 0)
	catchupNodes := make([]*node.Node, 0)
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
		return catchupNodes[i].GetRTT() < catchupNodes[j].GetRTT()
	})
	sort.SliceStable(archNodes, func(i, j int) bool {
		return archNodes[i].GetRTT() < archNodes[j].GetRTT()
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
			gs.log.Warnf("Cluster state updated, synced nodes: %d+%d ", len(gs.catchupNodes), len(gs.archNodes))
		case <-ctx.Done():
			return
		}
	}
}

func (gs *NodeCluster) isBlockInTheFuture(round uint64) bool {
	gs.RLock()
	defer gs.RUnlock()
	return round > gs.latestRound
}

//GetLatestRound returns latest round available on the cluster
func (gs *NodeCluster) LatestRoundGet() uint64 {
	gs.RLock()
	lr := gs.latestRound
	gs.RUnlock()
	return lr
}

//SetLatestRound sets latest round available on the cluster
func (gs *NodeCluster) LatestRoundSet(lr uint64, ns *models.NodeStatus) bool {
	gs.Lock()
	defer gs.Unlock()
	if lr > gs.latestRound {
		gs.latestRound = lr
		gs.lastAt = time.Now()
		gs.latestStatus = ns
		gs.broadcaster.TrySubmit(ns)
		return true
	}
	return false
}

func (gs *NodeCluster) WaitForStatusAfter(ctx context.Context, round uint64) *models.NodeStatus {
	var lrStatus *models.NodeStatus
	gs.RLock()
	lr := gs.latestRound
	if gs.latestStatus != nil {
		lrStatus = gs.latestStatus
	}
	gs.RUnlock()
	if lrStatus != nil && lr > round {
		return lrStatus
	}
	ch := make(chan interface{})
	gs.broadcaster.Register(ch)
	defer gs.broadcaster.Unregister(ch)
	for {
		select {
		case <-ctx.Done():
			return nil
		case status := <-ch:
			nodeStatus := status.(*models.NodeStatus)
			if nodeStatus.LastRound > round {
				return nodeStatus
			}
		}
	}
}

//EnsureGenesis returns error if supplied genesis hash does not match cluster genesis
func (gs *NodeCluster) GenesisEnsure(g string) error {
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

func (gs *NodeCluster) FatalError(err error) {
	select {
	case gs.fatalErr <- err:
	default:
	}
}

func (gs *NodeCluster) ProxyHTTP(c echo.Context, proxyStatuses []int) error {
	nodes := gs.getSyncedNodesByRTT()
	for i, n := range nodes {
		if i < len(nodes)-1 {
			//Proxy only if specific status is returned
			if ok, _, _ := n.ProxyHTTP(c, utils.Proxy404); ok {
				return nil
			}
		} else {
			//Proxy any status from last node (fallback)
			if ok, _, _ := n.ProxyHTTP(c, utils.ProxyALL); ok {
				return nil
			}
		}
	}

	return utils.JsonError(c, http.StatusBadGateway, "No synced upstream nodes available")

}

func (gs *NodeCluster) GetBlockWrap(ctx context.Context, round uint64, msgp bool) (*blockwrap.BlockWrap, error) {
	if gs.isBlockInTheFuture(round) {
		return nil, errors.New(ERRMSG_BLK404)
	}
	if msgp {
		gs.prefetchNotify(round)
	}
	return gs.ucache.GetBlock(ctx, round)
}

func (gs *NodeCluster) isBlockCached(round uint64) bool {
	return gs.ucache.IsBlockCached(round)
}

func (gs *NodeCluster) StateUpdate() {
	gs.cState <- struct{}{}
}

func (gs *NodeCluster) StateIsReady() bool {
	gs.RLock()
	defer gs.RUnlock()
	return len(gs.catchupNodes)+len(gs.archNodes) > 0
}

//LoadBlockSync blocks until the round is loaded into cache or the load fails
func (gs *NodeCluster) LoadBlockSync(ctx context.Context, round uint64) bool {
	//TODO
	//handle future rounds
	//handle parallel limit
	if round > gs.latestRound {
		gs.log.Errorf("request block %d greater than lastRound", round)
		gs.BlockSinkError(round, "cluster", nil)
		return false
	}
	if round > gs.latestRound-blockcache.CatchupSize+4 {
		gs.log.Debugf("fetching block %d starting with catchup nodes", round)
		for _, n := range gs.catchupNodes {
			if n.FetchBlockRaw(ctx, round) {
				return true
			}
		}
		gs.log.Debugf("falling back to archive nodes for block %d", round)
		for _, n := range gs.archNodes {
			if n.FetchBlockRaw(ctx, round) {
				return true
			}
		}
		gs.log.Errorf("all nodes unavailable to load block %d", round)
		return false
	}
	for _, n := range gs.archNodes {
		if n.FetchBlockRaw(ctx, round) {
			return true
		}
	}
	gs.log.Errorf("all archival nodes unavailable to load block %d", round)
	return false
}

func (gs *NodeCluster) addNode(ctx context.Context, cfg *config.NodeCfg) error {
	node, err := node.New(ctx, cfg, gs, gs.log)
	if err != nil {
		return err
	}
	gs.Lock()
	gs.nodes = append(gs.nodes, node)
	gs.Unlock()
	return nil
}

func (gs *NodeCluster) BlockSinkError(round uint64, src string, err error) {
	if err == nil {
		err = errors.New(ERRMSG_BLK404)
	}
	if gs.ucache != nil {
		gs.ucache.AddBlock(
			blockwrap.MakeBlockWrap(round, src, nil, err)
		)
	}
}

//New instantiates all configured nodes and returns new node cluster object
func New(ctx context.Context, ucache *blockcache.UnifiedBlockCache, cfg config.AlgoVNodeConfig, log *logrus.Entry) *node.NodeCluster {
	cluster := &NodeCluster{
		genesis:      "",
		latestRound:  0,
		fatalErr:     make(chan error),
		nodes:        make([]*node.Node, 0),
		ucache:       ucache,
		log:          log,
		catchupNodes: make([]*node.Node, 0),
		archNodes:    make([]*node.Node, 0),
		cState:       make(chan struct{}, 100),
		broadcaster:  broadcast.NewBroadcaster(1),
		bpSink:       make(chan uint64, 100),
		bps:          make([]*BlockPrefetch, 0),
		up:           false,
	}

	for _, n := range cfg.Algod.Nodes {
		cluster.addNode(ctx, n)
	}

	go cluster.broadcastListener(ctx)

	go cluster.stateChangeMonitor(ctx)

	go cluster.prefetchMonitor(ctx)

	return cluster

}

func init() {
	http.DefaultTransport.(*http.Transport).MaxIdleConns = 150
	http.DefaultTransport.(*http.Transport).MaxConnsPerHost = 150
	http.DefaultTransport.(*http.Transport).MaxIdleConnsPerHost = 150
}
