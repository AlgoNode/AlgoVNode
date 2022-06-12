package blockwrap

import (
	"errors"
	"time"

	"github.com/algonode/algovnode/internal/utils"
	"github.com/algorand/go-algorand/protocol"
	"github.com/algorand/go-algorand/rpcs"
)

type BlockWrap struct {
	Round uint64
	Raw   []byte
	Src   string
	Error error
}

func (bw *BlockWrap) Decoded() (*rpcs.EncodedBlockCert, error) {
	if bw.Raw != nil {
		block := new(rpcs.EncodedBlockCert)
		if err := protocol.Decode(bw.Raw, block); err != nil {
			return nil, err
		}
		return block, nil
	}
	if bw.Error != nil {
		return nil, bw.Error
	}
	return nil, errors.New("Missing block data")
}

func (bw *BlockWrap) AsNodeJson() ([]byte, error) {
	dBlock, err := bw.Decoded()
	if err != nil {
		return nil, err
	}
	jBlock, err := utils.EncodeJson(dBlock)
	if err != nil {
		return nil, err
	}
	return jBlock, nil
}

func (bw *BlockWrap) AsIdxJson() ([]byte, error) {
	dBlock, err := bw.Decoded()
	if err != nil {
		return nil, err
	}
	blockIdx, err := utils.GenerateBlock(dBlock)
	if err != nil {
		return nil, err
	}

	idxJBlock, err := utils.EncodeJson(*blockIdx)
	if err != nil {
		return nil, err
	}

}

func MakeBlockWrap(src string, block *rpcs.EncodedBlockCert, blockRaw []byte) (*BlockWrap, error) {

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
