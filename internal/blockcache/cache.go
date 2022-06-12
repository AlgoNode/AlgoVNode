package blockcache

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/algonode/algovnode/internal/blockwrap"
	cache "github.com/hashicorp/golang-lru"
	"github.com/sirupsen/logrus"
)

type BlockEntry struct {
	sync.RWMutex
	blockwrap.BlockWrap
	WaitFor chan struct{}
}

type BlockCache struct {
	c    *cache.Cache
	last uint64
	name string
}

func (bc *BlockCache) promiseBlock(round uint64) *BlockEntry {
	//under ubc lock
	be := &BlockEntry{
		WaitFor:   make(chan struct{}),
		BlockWrap: blockwrap.BlockWrap{Round: round},
	}
	if fbe, ok, _ := bc.c.PeekOrAdd(be.Round, be); ok {
		bentry := fbe.(*BlockEntry)
		bentry.Lock()
		if bentry.Error != nil {
			logrus.Tracef("Rearming cache entry for block %d", round)
			bentry.WaitFor = make(chan struct{})
			bentry.Error = nil
		} else {
			logrus.Tracef("Block %d already promised", round)
		}
		bentry.Unlock()
		return bentry
	} else {
		logrus.Tracef("New promise for block %d", round)
	}
	return be
}

func (bc *BlockCache) addBlock(b *blockwrap.BlockWrap) {
	be := &BlockEntry{
		BlockWrap: *b,
		WaitFor:   nil,
	}
	if e, found, _ := bc.c.PeekOrAdd(be.Round, be); found {
		fbe := e.(*BlockEntry)
		fbe.Lock()

		//If the block is not cached yet
		if fbe.Raw == nil {
			//If we have block data
			if b.Error == nil {
				fbe.BlockWrap = *b
				logrus.Tracef("Delivered block %d to cache %s", b.Round, bc.name)
			} else {
				//Or this is just an error
				if fbe.Error == nil {
					logrus.Infof("Delivered block %d to cache %s with err %s", b.Round, bc.name, b.Error)
					fbe.Error = b.Error
				}
			}
		} else {
			logrus.Tracef("Block %d already cached in %s", b.Round, bc.name)
		}
		//notify waiters
		if (fbe.Raw != nil || fbe.Error != nil) && fbe.WaitFor != nil {
			logrus.Tracef("notifying watchers for block %d", b.Round)
			close(fbe.WaitFor)
			fbe.WaitFor = nil
		}
		fbe.Unlock()
	} else {
		logrus.Tracef("Added block %d to cache %s ", b.Round, bc.name)
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
		return be.(*BlockEntry).Raw != nil
	}
	return false
}

func (bc *BlockCache) getBlockEntry(ctx context.Context, round uint64) (*BlockEntry, bool, error) {
	if item, ok := bc.c.Get(round); ok {
		be := item.(*BlockEntry)
		be.RLock()
		if be.Error != nil {
			defer be.RUnlock()
			return nil, false, be.Error
		}
		if be.Raw != nil {
			defer be.RUnlock()
			return be, true, nil
		}
		wf := be.WaitFor
		be.RUnlock()
		if wf == nil {
			logrus.Tracef("block %d not loaded and not promised", round)
			return nil, false, errors.New("block not scheduled for load")
		}
	TheLoop:
		for {
			select {
			case <-time.Tick(time.Second):
				logrus.Tracef("Waiting tick for promised round %d", round)
			case <-wf:
				break TheLoop
			case <-ctx.Done():
				bc.addBlock(&blockwrap.BlockWrap{
					Round: round,
					Error: errors.New("block timeout"),
				})
				return nil, false, ctx.Err()
			}
		}
		be.RLock()
		defer be.RUnlock()
		if be.Error != nil {
			return nil, false, be.Error
		}
		return be, true, nil
	}
	return nil, false, nil
}
