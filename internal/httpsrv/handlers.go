package httpsrv

import (
	"github.com/algonode/algovnode/internal/algod"
	"github.com/algonode/algovnode/internal/blockcache"
	"github.com/labstack/echo/v4"
	log "github.com/sirupsen/logrus"
)

type ServerImplementation struct {
	log     *log.Logger
	ucache  *blockcache.UnifiedBlockCache
	cluster *algod.NodeCluster
}

func (si *ServerImplementation) waitHandler(c echo.Context) error {
	return nil
}

func (si *ServerImplementation) defaultHandler(c echo.Context) error {
	//si.cluster.
	return nil
}

func (si *ServerImplementation) blocksHandler(c echo.Context) error {
	return nil
}

func RegisterHandlersAuth(r *echo.Echo, si *ServerImplementation, m ...echo.MiddlewareFunc) {
	r.GET("/v2/status/wait-for-block-after", si.waitHandler, m...)
	r.GET("/v2/blocks/:round", si.blocksHandler, m...)

	r.GET("/v2/blocks/:round/transactions", si.defaultHandler, m...)
	r.GET("/", si.defaultHandler, m...)
	r.POST("/", si.defaultHandler, m...)
	r.HEAD("/", si.defaultHandler, m...)
	r.OPTIONS("/", si.defaultHandler, m...)
}

func RegisterHandlersNoAuth(r *echo.Echo, si *ServerImplementation) {
	r.GET("/health", si.defaultHandler)
}
