package algod

import (
	"context"
	"time"
)

const BP_COUNT = 30

type BlockPrefetch struct {
	FirstReq  uint64
	LastReq   uint64
	LastPre   uint64
	NextPre   uint64
	Hits      uint64
	LastPreAt time.Time
}

func (gs *NodeCluster) prefetchMonitor(ctx context.Context) {
	go func() {
		for {
			select {
			case round := <-gs.bpSink:
				gs.prefetchDetect(ctx, round)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (gs *NodeCluster) prefetchNotify(round uint64) {
	select {
	case gs.bpSink <- round:
	default:
	}
}

func (gs *NodeCluster) prefetchPrune() {
	expire := time.Now().Add(time.Minute * time.Duration(-1))
	//Optimistic loop
	for _, b := range gs.bps {
		if b.LastPreAt.Before(expire) {
			nbps := make([]*BlockPrefetch, 0)
			//We have expired prefetchers - do full prunning
			for i := range gs.bps {
				if gs.bps[i].LastPreAt.Before(expire) {
					gs.log.Tracef("Prunning indexer prefetch for blocks %d...%d", gs.bps[i].FirstReq, gs.bps[i].LastReq)
				} else {
					nbps = append(nbps, gs.bps[i])
				}
			}
			gs.bps = nbps
			return
		}
	}
}

func (gs *NodeCluster) prefetchDetect(ctx context.Context, round uint64) {
	var bpf *BlockPrefetch = nil

	for _, bp := range gs.bps {
		if round >= bp.FirstReq && round <= bp.LastReq+1 {
			bpf = bp
			break
		}
	}

	if bpf == nil {
		bpf = &BlockPrefetch{FirstReq: round, LastPre: round, LastReq: round, Hits: 0, NextPre: 0, LastPreAt: time.Now()}
		gs.bps = append(gs.bps, bpf)
	}

	bpf.LastReq = round
	bpf.Hits++

	if bpf.Hits > 2 {
		if bpf.NextPre == 0 {
			preFrom := bpf.LastPre + 1
			preTo := bpf.LastPre + BP_COUNT
			gs.log.Warnf("Detected indexer sync, prefetching %d...%d", preFrom, preTo)
			for i := preFrom; i <= preTo; i++ {
				if !gs.ucache.IsBlockCached(round) {
					gs.ucache.PromiseBlock(i)
				}
			}
			bpf.LastPre = preTo
			bpf.LastPreAt = time.Now()
			bpf.NextPre = round + BP_COUNT/2
		} else {
			if round > bpf.NextPre {
				preFrom := bpf.LastPre + 1
				preTo := bpf.LastPre + BP_COUNT/2
				gs.log.Warnf("Indexer sync @%d prefetch %d...%d", round, preFrom, preTo)
				for i := preFrom; i <= preTo; i++ {
					gs.ucache.PromiseBlock(i)
				}
				bpf.LastPre = preTo
				bpf.LastPreAt = time.Now()
				bpf.NextPre += BP_COUNT / 2
			}
		}
	}

	gs.prefetchPrune()

}
