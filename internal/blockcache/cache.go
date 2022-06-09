package blockcache

import (
	"context"
	"errors"
	"sync"

	"github.com/algonode/algovnode/internal/blockfetcher"
	cache "github.com/hashicorp/golang-lru"
	"github.com/sirupsen/logrus"
)

type BlockEntry struct {
	sync.RWMutex
	B       *blockfetcher.BlockWrap
	WaitFor chan struct{}
	Round   uint64
	Error   error
}

type BlockCache struct {
	c    *cache.Cache
	last uint64
	name string
}

func (bc *BlockCache) promiseBlock(round uint64) *BlockEntry {
	//under ubc lock
	be := &BlockEntry{
		B:       nil,
		WaitFor: make(chan struct{}),
		Round:   uint64(round),
	}
	if fbe, ok, _ := bc.c.PeekOrAdd(be.Round, be); ok {
		bentry := fbe.(*BlockEntry)
		bentry.Lock()
		if bentry.Error != nil {
			//FIXME: danger
			//rearm
			bentry.WaitFor = make(chan struct{})
			bentry.Error = nil
		}
		bentry.Unlock()
		return bentry
	}
	return be
}

func (bc *BlockCache) addBlock(b *blockfetcher.BlockWrap) {
	be := &BlockEntry{
		B:       nil,
		WaitFor: nil,
		Round:   uint64(b.Round),
		Error:   b.Error,
	}
	if b.Error == nil {
		be.B = b
	}
	if e, found, _ := bc.c.PeekOrAdd(be.Round, be); found {
		fbe := e.(*BlockEntry)
		fbe.Lock()

		//If the block is not cached yet
		if fbe.B == nil {
			//If we have block data
			if b.Error == nil {
				fbe.B = b
				logrus.Tracef("Added block %d to cache %s", b.Round, bc.name)
			} else {
				//Or this is just an error
				if fbe.Error == nil {
					logrus.Infof("Added block %d to cache %s with err %s", b.Round, bc.name, b.Error)
					fbe.Error = b.Error
				}
			}
		}
		//notify waiters
		if (fbe.B != nil || fbe.Error != nil) && fbe.WaitFor != nil {
			logrus.Tracef("notifying watchers for block %d", b.Round)
			close(fbe.WaitFor)
			fbe.WaitFor = nil
		}
		fbe.Unlock()
	}
	if bc.last < b.Round && b.Error == nil {
		bc.last = b.Round
	}
}

func (bc *BlockCache) IsBlockPromised(round uint64) bool {
	if _, ok := bc.c.Peek(round); ok {
		return true
	}
	return false
}

func (bc *BlockCache) IsBlockCached(round uint64) bool {
	if be, ok := bc.c.Peek(round); ok {
		be.(*BlockEntry).RLock()
		defer be.(*BlockEntry).RUnlock()
		return be.(*BlockEntry).B != nil
	}
	return false
}

func (bc *BlockCache) getBlock(ctx context.Context, round uint64) (*blockfetcher.BlockWrap, bool, error) {
	if item, ok := bc.c.Get(round); ok {
		be := item.(*BlockEntry)
		be.RLock()
		if be.Error != nil {
			defer be.RUnlock()
			return nil, false, be.Error
		}
		if be.B != nil {
			defer be.RUnlock()
			return be.B, true, nil
		}
		wf := be.WaitFor
		be.RUnlock()
		if wf == nil {
			return nil, false, errors.New("block not scheduled for load")
		}
		select {
		case <-wf:
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
		be.RLock()
		defer be.RUnlock()
		if be.Error != nil {
			return nil, false, be.Error
		}
		return be.B, true, nil
	}
	return nil, false, nil
}
