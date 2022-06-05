package httpsrv

import (
	"errors"
	"net/http"
	"strconv"

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

//TODO: tune this per endpoint
var proxy404 = []int{200, 204, 400, 404}
var proxy200 = []int{200, 204, 400}
var proxyALL []int = nil

func (si *ServerImplementation) waitHandler(c echo.Context) error {
	round := c.Param("round")
	roundInt64, err := strconv.ParseInt(round, 10, 64)
	if round == "" || err != nil || roundInt64 < 0 {
		jsonError(c, http.StatusBadRequest, "Invalid round value")
		return nil
	}
	status := si.cluster.WaitForStatusAfter(c.Request().Context(), uint64(roundInt64))
	if status == nil {
		jsonError(c, http.StatusInternalServerError, "Error waiting for round")
		return nil
	}
	return c.JSON(http.StatusOK, status)
}

func jsonError(c echo.Context, status int, err string) {
	c.JSON(status, map[string]string{"message": err})
}

func (si *ServerImplementation) defaultHandler(c echo.Context) error {

	nodes := si.cluster.GetSyncedNodesByRTT()
	for i, n := range nodes {
		if i < len(nodes)-1 {
			//Proxy only if specific status is returned
			if ok, _, _ := n.ProxyHTTP(c, proxy404); ok {
				return nil
			}
		} else {
			//Proxy any status from last node (fallback)
			if ok, _, _ := n.ProxyHTTP(c, proxyALL); ok {
				return nil
			}
		}
	}

	jsonError(c, http.StatusBadGateway, "No synced upstream nodes available")

	return errors.New("no synced upstream nodes available")
}

func (si *ServerImplementation) blocksHandler(c echo.Context) error {
	return nil
}

func RegisterHandlersAuth(r *echo.Echo, si *ServerImplementation, m ...echo.MiddlewareFunc) {
	r.GET("/v2/status/wait-for-block-after/:round", si.waitHandler, m...)
	r.GET("/v2/blocks/:round", si.blocksHandler, m...)

	//TODO: handle this endpoint internally
	r.GET("/v2/blocks/:round/transactions", si.defaultHandler, m...)

	r.GET("/", si.defaultHandler, m...)
	r.POST("/", si.defaultHandler, m...)
	r.HEAD("/", si.defaultHandler, m...)
	r.OPTIONS("/", si.defaultHandler, m...)
}

func RegisterHandlersNoAuth(r *echo.Echo, si *ServerImplementation) {
	r.GET("/health", si.defaultHandler)
}
