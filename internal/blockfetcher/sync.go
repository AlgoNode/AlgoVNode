package blockfetcher

import (
	"context"
	"time"

	"github.com/algonode/algovnode/internal/utils"
	"github.com/algorand/go-algorand/rpcs"
)

type BlockWrap struct {
	Round         uint64
	BlockMsgPack  []byte
	Block         *rpcs.EncodedBlockCert
	BlockJsonIdx  string
	BlockJsonNode string
	Src           string
	CachedAt      time.Time
	Error         error
}

type BlockFetcher interface {
	LoadBlockSync(ctx context.Context, round uint64) bool
}

func MakeBlockWrap(src string, block *rpcs.EncodedBlockCert, blockRaw []byte) (*BlockWrap, error) {
	jBlock, err := utils.EncodeJson(block)
	if err != nil {
		return nil, err
	}

	blockIdx, err := utils.GenerateBlock(&block.Block)
	if err != nil {
		return nil, err
	}

	idxJBlock, err := utils.EncodeJson(*blockIdx)
	if err != nil {
		return nil, err
	}

	bw := &BlockWrap{
		Round:         uint64(block.Block.BlockHeader.Round),
		CachedAt:      time.Now(),
		Block:         block,
		BlockMsgPack:  blockRaw,
		BlockJsonIdx:  string(idxJBlock),
		BlockJsonNode: string(jBlock),
		Src:           src,
	}
	return bw, nil

}
