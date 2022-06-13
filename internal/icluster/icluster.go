package icluster

import (
	"context"

	"github.com/algonode/algovnode/internal/blockwrap"
	"github.com/algorand/go-algorand-sdk/client/v2/common/models"
	"github.com/labstack/echo/v4"
)

type Cluster interface {
	StateIsReady() bool
	WaitForStatusAfter(ctx context.Context, round uint64) *models.NodeStatus
	WaitForFatal(ctx context.Context)
	ProxyHTTP(c echo.Context, proxyStatuses []int) error
	GetBlockWrap(ctx context.Context, round uint64, msgp bool) (*blockwrap.BlockWrap, error)
}
