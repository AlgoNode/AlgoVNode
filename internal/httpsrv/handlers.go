package httpsrv

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/algonode/algovnode/internal/algod"
	"github.com/algonode/algovnode/internal/blockcache"
	"github.com/algonode/algovnode/internal/utils"
	"github.com/labstack/echo/v4"
	log "github.com/sirupsen/logrus"
)

type ServerImplementation struct {
	log     *log.Logger
	ucache  *blockcache.UnifiedBlockCache
	cluster *algod.NodeCluster
}

type BlockFormat int64

const (
	BFMsgPack BlockFormat = iota
	BFNodeJson
	BFIdxJson
)

//waitHandler waits for round after
func (si *ServerImplementation) waitHandler(c echo.Context) error {
	round := c.Param("round")
	roundInt64, err := strconv.ParseInt(round, 10, 64)
	if round == "" || err != nil || roundInt64 < 0 {
		utils.JsonError(c, http.StatusBadRequest, "Invalid round value")
		return nil
	}

	timeoutCtx, cancel := context.WithTimeout(c.Request().Context(), time.Second*10)
	defer cancel()

	status := si.cluster.WaitForStatusAfter(timeoutCtx, uint64(roundInt64))
	if timeoutCtx.Err() == context.DeadlineExceeded {
		utils.JsonError(c, http.StatusServiceUnavailable, "timeout")
		return nil
	}

	if status == nil {
		utils.JsonError(c, http.StatusInternalServerError, "Error waiting for round")
		return nil
	}

	return c.JSON(http.StatusOK, status)
}

func (si *ServerImplementation) defaultHandler(c echo.Context) error {
	return si.cluster.ProxyHTTP(c, utils.Proxy404)
}

func (si *ServerImplementation) blockHandlerWrap(c echo.Context) error {

	formatStr := "json"
	err := echo.QueryParamsBinder(c).String("format", &formatStr).BindError()
	if err != nil {
		utils.JsonError(c, http.StatusBadRequest, fmt.Sprintf("Invalid format for parameter format: %s", err))
		return nil
	}

	format := BFNodeJson
	formatStr = strings.ToLower(formatStr)

	switch formatStr {
	case "json":
		format = BFNodeJson
	case "msgpack":
		fallthrough
	case "msgp":
		format = BFMsgPack
	case "idx":
		fallthrough
	case "indexer":
		format = BFIdxJson
	default:
		utils.JsonError(c, http.StatusBadRequest, fmt.Sprintf("Invalid format: %s", formatStr))
		return nil
	}
	return si.blocksHandler(c, format)
}

func (si *ServerImplementation) blockIdxHandlerWrap(c echo.Context) error {
	return si.blocksHandler(c, BFIdxJson)
}

func (si *ServerImplementation) blocksHandler(c echo.Context, format BlockFormat) error {

	round := c.Param("round")
	roundInt64, err := strconv.ParseInt(round, 10, 64)
	if round == "" || err != nil || roundInt64 < 0 {
		utils.JsonError(c, http.StatusBadRequest, "Invalid round value")
		return nil
	}

	timeoutCtx, cancel := context.WithTimeout(c.Request().Context(), time.Second*10)
	defer cancel()

	block, err := si.cluster.GetBlock(timeoutCtx, uint64(roundInt64), format == BFMsgPack)

	if timeoutCtx.Err() == context.DeadlineExceeded {
		utils.JsonError(c, http.StatusServiceUnavailable, "timeout")
		return nil
	}

	if err != nil {
		utils.JsonError(c, http.StatusInternalServerError, err.Error())
		return nil
	}

	if block != nil {
		//block.BlockJsonNode
		switch format {
		case BFMsgPack:
			c.Response().Header().Set("X-Algorand-Struct", "block-v1")
			c.Blob(http.StatusOK, "application/msgpack", block.BlockMsgPack)
		case BFNodeJson:
			c.JSONBlob(http.StatusOK, []byte(block.BlockJsonNode))
		case BFIdxJson:
			c.JSONBlob(http.StatusOK, []byte(block.BlockJsonIdx))
		}
		return nil
	}

	return c.JSON(http.StatusNoContent, nil)
}

func RegisterAlgodHandlers(r *echo.Echo, si *ServerImplementation, m ...echo.MiddlewareFunc) {
	r.GET("/v2/status/wait-for-block-after/:round", si.waitHandler, m...)
	r.GET("/v2/blocks/:round", si.blockHandlerWrap, m...)

	//TODO: handle this endpoint internally
	r.GET("/v2/blocks/:round/transactions", si.defaultHandler, m...)

	r.GET("/*", si.defaultHandler, m...)
	r.POST("/*", si.defaultHandler, m...)
	r.HEAD("/*", si.defaultHandler, m...)
	r.OPTIONS("/*", si.defaultHandler, m...)

	//no auth
	r.GET("/health", si.defaultHandler)
}

func RegisterIdxHandlers(r *echo.Echo, si *ServerImplementation, m ...echo.MiddlewareFunc) {
	r.GET("/v2/blocks/:round", si.blockIdxHandlerWrap, m...)

	//no auth
	//TODO: implement me please
	//r.GET("/v2/status", si.statusIdxHandler)
}
