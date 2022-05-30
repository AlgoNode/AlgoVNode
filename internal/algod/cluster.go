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
	"sync"
	"time"

	"github.com/algonode/algovnode/internal/blockcache"
	"github.com/algonode/algovnode/internal/config"
	"github.com/sirupsen/logrus"
)

type NodeCluster struct {
	sync.Mutex
	fatalErr    chan error
	ucache      *blockcache.UnifiedBlockCache
	latestRound uint64
	lastSrc     *Node
	lastAt      time.Time
	genesis     string
	nodes       []*Node
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
		logrus.WithError(err).Error("Quitting due to fatal error")
	}
}

//LoadBlock does
func (gs *NodeCluster) LoadBlock(ctx context.Context, round uint64) {
	//TODO
	//handle future rounds
	//handle parallel limit
	if round > gs.latestRound {
		gs.fatalErr <- errors.New("Tried to load future block")
	} else {
		if round < gs.latestRound-blockcache.CatchupSize+2 {
			fetches := 0
			for _, n := range gs.nodes {
				//skip for some node states
				if n.catchup && n.state != AnsFailed {
					//try all catchup in parallel
					go n.FetchBlockRaw(ctx, round)
					fetches++
				}
			}
			if fetches == 0 {
				for _, n := range gs.nodes {
					//skip for some node states
					if !n.catchup && n.state != AnsFailed {
						//try all archive in parallel
						go n.FetchBlockRaw(ctx, round)
						fetches++
					}
				}
			}
			if fetches == 0 {
				logrus.Errorf("All nodes unavailable to load block %d", round)
			} else {
				logrus.Debugf("Fetching block %d on %d nodes", round, fetches)
			}
		} else {
			fetches := 0
			for _, n := range gs.nodes {
				//skip for some node states
				if !n.catchup && n.state != AnsFailed {
					//try all archive in parallel
					n.FetchBlockRaw(ctx, round)
					fetches++
				}
			}
			if fetches == 0 {
				logrus.Errorf("All nodes unavailable to load block %d", round)
			} else {
				logrus.Debugf("Fetching block %d on %d nodes", round, fetches)
			}
		}
	}
}

//NewCluster instantiates all configured nodes and returns new node cluster object
func NewCluster(ctx context.Context, ucache *blockcache.UnifiedBlockCache, cfg config.AlgoVNodeConfig) *NodeCluster {
	cluster := &NodeCluster{
		genesis:     "",
		latestRound: 0,
		fatalErr:    make(chan error),
		nodes:       make([]*Node, 0),
		ucache:      ucache,
	}

	for _, n := range cfg.Algod.Nodes {
		cluster.AddNode(ctx, n)
	}
	return cluster

}
