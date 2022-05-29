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

	"github.com/algonode/algovnode/internal/blockfetcher"
	cache "github.com/hashicorp/golang-lru"
)

const (
	CatchupSize = 1000
	ArchSize    = 1000
)

type GlobalState struct {
	sync.Mutex
	catchupCache *BlockCache
	archCache    *BlockCache
}

var gState GlobalState

func blockSinkProcessor(ctx context.Context, bs chan *blockfetcher.BlockWrap) {
TheLoop:
	for {
		select {
		case <-ctx.Done():
			break TheLoop
		case b := <-bs:
			if gState.catchupCache.last != 0 || b.Round < gState.catchupCache.last-CatchupSize {
				gState.catchupCache.addBlock(b)
			} else {
				gState.archCache.addBlock(b)
			}
		}
	}
}

func StartBlockSink(ctx context.Context) chan *blockfetcher.BlockWrap {
	cc, _ := cache.New(CatchupSize)
	gState.catchupCache = &BlockCache{c: cc, last: 0}
	ca, _ := cache.New(ArchSize)
	gState.archCache = &BlockCache{c: ca, last: 0}

	bs := make(chan *blockfetcher.BlockWrap, gState.catchupCache.last)
	go blockSinkProcessor(ctx, bs)
	return bs
}
