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
	"github.com/algonode/algovnode/internal/blockfetcher"
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

func (gs *NodeCluster) GetLatestRound() uint64 {
	gs.Lock()
	lr := gs.latestRound
	gs.Unlock()
	return lr
}

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

func (gs *NodeCluster) HandleFatal(ctx context.Context) {
	select {
	case <-ctx.Done():
	case err := <-gs.fatalErr:
		logrus.WithError(err).Error("Quitting due to fatal error")
	}
}

func (gs *NodeCluster) GetBlock(ctx context.Context, round uint64) (*blockfetcher.BlockWrap, error) {
	if round > gs.latestRound {

	} else {
		if round < gs.latestRound-blockcache.CatchupSize+1 {

		} else {

		}
	}
	return nil, nil
}

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
