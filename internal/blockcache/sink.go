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
	"time"

	"github.com/algorand/go-algorand-sdk/types"
	cache "github.com/hashicorp/golang-lru"
)

type BlockWrap struct {
	Round         uint64
	BlockMsgPack  []byte
	Block         *types.Block
	BlockJsonIdx  *string
	BlockJsonNode *string
	Src           string
	CachedAt      time.Time
}

type BlockEntry struct {
	B       *BlockWrap
	WaitFor chan struct{}
	Round   uint64
}

type GlobalState struct {
	sync.Mutex
	catchupCache *cache.Cache
	archCache    *cache.Cache
}

var gState GlobalState

func init() {
	//never errs
	gState.catchupCache, _ = cache.New(8)
	gState.archCache, _ = cache.New(8)
}

func (b *BlockWrap) cacheCatchupBlock() {
	be := &BlockEntry{
		B:       b,
		WaitFor: make(chan struct{}),
		Round:   uint64(b.Block.Round),
	}
	if ok, _ := gState.catchupCache.ContainsOrAdd(be.Round, be); ok {
		//already in the cache
		if e, found := gState.catchupCache.Peek(be.Round); found {
			if e.(*BlockEntry).B == nil && e.(*BlockEntry).WaitFor != nil {
				e.(*BlockEntry).B = be.B
				//notify waiters
				close(e.(*BlockEntry).WaitFor)
			}
		}
	}

}

func blockSinkProcessor(ctx context.Context, bs chan *BlockWrap) {
TheLoop:
	for {
		select {
		case <-ctx.Done():
			break TheLoop
		case b := <-bs:
			b.cacheCatchupBlock()
		}
	}
}

func StartBlockSink(ctx context.Context) chan *BlockWrap {
	bs := make(chan *BlockWrap, 1000)
	go blockSinkProcessor(ctx, bs)
	return bs
}
