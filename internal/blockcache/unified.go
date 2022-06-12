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

package blockcache

import (
	"context"
	"sync"

	"github.com/algonode/algovnode/internal/blockwrap"
	cache "github.com/hashicorp/golang-lru"
	"github.com/sirupsen/logrus"
)

//TODO:
// configurable cache sizes
// replace LRU with FIFO + RTT for arch
const (
	CatchupSize = 1000
	ArchSize    = 1000
)

// UnifiedBlockCache describes a set of block caches.
type UnifiedBlockCache struct {
	sync.Mutex
	// catchupCache stores fresh blocks up to the current one.
	catchupCache *BlockCache
	// archCache stores random archival blocks including ones prefetched during indexer sync.
	archCache *BlockCache
	// bf holds pointer to a block fetch interface used in case of cache miss
}

//getCache returns pointer to a block cache appropriate for the block round
func (ubc *UnifiedBlockCache) getCacheWithPeek(round uint64) *BlockCache {
	if round == 0 || ubc.archCache.IsBlockPromised(round) {
		return ubc.archCache
	}
	if round > 0 && (ubc.catchupCache.last == 0 || round > ubc.catchupCache.last-CatchupSize) {
		return ubc.catchupCache
	}
	return ubc.archCache
}

//addBlock adds a wrapped block to the unified block cache
func (ubc *UnifiedBlockCache) AddBlock(b *blockwrap.BlockWrap) {
	ubc.Lock()
	defer ubc.Unlock()
	//todo - which cache
	ubc.getCacheWithPeek(b.Round).addBlock(b)
}

func (ubc *UnifiedBlockCache) PromiseBlock(round uint64) *BlockEntry {
	c := ubc.getCacheWithPeek(round)
	logrus.Debugf("Promising block %d in %s cache", round, c.name)
	return c.promiseBlock(round)
}

func (ubc *UnifiedBlockCache) IsBlockCached(round uint64) bool {
	return ubc.catchupCache.IsBlockCached(round) || ubc.archCache.IsBlockCached(round)
}

//GetBlock reads cached block or blocks till one is fetched from the cluster
func (ubc *UnifiedBlockCache) GetBlockEntry(ctx context.Context, round uint64) (*BlockEntry, bool, error) {
	if be, _, _ := ubc.catchupCache.getBlockEntry(ctx, round); be.Raw != nil {
		return be, true, nil
	}
	if be, _, _ := ubc.archCache.getBlockEntry(ctx, round); be.Raw != nil {
		return be, true, nil
	}
	return ubc.PromiseBlock(round), false, nil
}

//New returns new Unified Block Cache struct
func New(ctx context.Context) *UnifiedBlockCache {
	cc, _ := cache.New(CatchupSize)
	ca, _ := cache.New(ArchSize)

	ubc := &UnifiedBlockCache{
		catchupCache: &BlockCache{c: cc, last: 0, name: "catchup"},
		archCache:    &BlockCache{c: ca, last: 0, name: "archive"},
	}

	return ubc
}
