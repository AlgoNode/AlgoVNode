package blockcache

import (
	"context"

	"github.com/algonode/algovnode/internal/blockfetcher"
	cache "github.com/hashicorp/golang-lru"
	"github.com/sirupsen/logrus"
)

type BlockEntry struct {
	B       *blockfetcher.BlockWrap
	WaitFor chan struct{}
	Round   uint64
}

type BlockCache struct {
	c    *cache.Cache
	last uint64
	name string
}

func (bc *BlockCache) promiseBlock(round uint64) *BlockEntry {
	logrus.Debugf("Promising block %d in %s cache", round, bc.name)
	be := &BlockEntry{
		B:       nil,
		WaitFor: make(chan struct{}),
		Round:   uint64(round),
	}
	if ok, _ := bc.c.ContainsOrAdd(be.Round, be); ok {
		if be, ok := bc.c.Get(be.Round); ok {
			return be.(*BlockEntry)
		}
	}
	return be
}

func (bc *BlockCache) addBlock(b *blockfetcher.BlockWrap) {
	be := &BlockEntry{
		B:       b,
		WaitFor: make(chan struct{}),
		Round:   uint64(b.Round),
	}
	close(be.WaitFor)
	if ok, _ := bc.c.ContainsOrAdd(be.Round, be); ok {
		//already in the cache
		if e, found := bc.c.Peek(be.Round); found {
			if e.(*BlockEntry).B == nil && e.(*BlockEntry).WaitFor != nil {
				e.(*BlockEntry).B = be.B
				//notify waiters
				close(e.(*BlockEntry).WaitFor)
			}
		}
	}
	if bc.last < b.Round {
		bc.last = b.Round
	}
}

func (bc *BlockCache) tryGetBlock(round uint64) (*blockfetcher.BlockWrap, bool) {
	if be, ok := bc.c.Get(round); ok {
		bw := be.(*BlockEntry).B
		return bw, true
	}
	return nil, false
}

func (bc *BlockCache) IsBlockPromised(round uint64) bool {
	if be, ok := bc.c.Get(round); ok {
		return be.(*BlockEntry).B == nil
	}
	return false
}

func (bc *BlockCache) getBlock(ctx context.Context, round uint64) (*blockfetcher.BlockWrap, bool) {
	if item, ok := bc.c.Get(round); ok {
		be := item.(*BlockEntry)
		if be.B != nil {
			return be.B, true
		}
		select {
		case <-be.WaitFor:
		case <-ctx.Done():
		}
		return be.B, true
	}
	return nil, false
}
