package icluster

import (
	"github.com/algonode/algovnode/internal/blockwrap"
	"github.com/algorand/go-algorand-sdk/client/v2/common/models"
)

type Cluster interface {
	BlockSink(*blockwrap.BlockWrap) bool
	StateUpdate()
	IsUp() bool
	LatestRoundGet() uint64
	LatestRoundSet(round uint64, status *models.NodeStatus)
}
