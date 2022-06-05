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
	"fmt"
	"sync"

	"github.com/algonode/algovnode/internal/blockfetcher"
	cache "github.com/hashicorp/golang-lru"
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
	bf blockfetcher.BlockFetcher
	// Sink exposes block sink for the cache
	Sink chan *blockfetcher.BlockWrap
}

//getCache returns pointer to a block cache appropriate for the block round
func (ubc *UnifiedBlockCache) getCache(round uint64) *BlockCache {
	if ubc.catchupCache.last != 0 || round < ubc.catchupCache.last-CatchupSize {
		return ubc.catchupCache
	}
	return ubc.archCache
}

//addBlock adds a wrapped block to the unified block cache
func (ubc *UnifiedBlockCache) addBlock(b *blockfetcher.BlockWrap) {
	ubc.Lock()
	defer ubc.Unlock()
	ubc.getCache(b.Round).addBlock(b)
}

func (ubc *UnifiedBlockCache) promiseBlock(round uint64) *BlockEntry {
	return ubc.getCache(round).promiseBlock(round)
}

//getBlock reads cached block or blocks till one is fetched from the cluster
func (ubc *UnifiedBlockCache) getBlock(ctx context.Context, round uint64) (*blockfetcher.BlockWrap, error) {
	ubc.Lock()
	if bw, _ := ubc.catchupCache.getBlock(ctx, round); bw != nil {
		ubc.Unlock()
		return bw, nil
	}
	if bw, _ := ubc.archCache.getBlock(ctx, round); bw != nil {
		ubc.Unlock()
		return bw, nil
	}
	ubc.promiseBlock(round)
	ubc.Unlock()
	ubc.bf.LoadBlock(ctx, round)
	if bw, _ := ubc.catchupCache.getBlock(ctx, round); bw != nil {
		return bw, nil
	}
	if bw, _ := ubc.archCache.getBlock(ctx, round); bw != nil {
		return bw, nil
	}
	return nil, fmt.Errorf("error getting block %d", round)
}

//SetBlockFetcher sets a block fetching interface for handling cache misses
func (ubc *UnifiedBlockCache) SetBlockFetcher(bf blockfetcher.BlockFetcher) {
	ubc.bf = bf
}

//New starts background block processing goroutine and returns new Unified Block Cache
func New(ctx context.Context) *UnifiedBlockCache {
	cc, _ := cache.New(CatchupSize)
	ca, _ := cache.New(ArchSize)
	bs := make(chan *blockfetcher.BlockWrap, CatchupSize)

	ubc := &UnifiedBlockCache{
		catchupCache: &BlockCache{c: cc, last: 0},
		archCache:    &BlockCache{c: ca, last: 0},
		Sink:         bs,
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case b := <-bs:
				ubc.addBlock(b)
			}
		}
	}()
	return ubc
}
