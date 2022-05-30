package httpsrv

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/algonode/algovnode/internal/algod"
	"github.com/algonode/algovnode/internal/blockcache"
	"github.com/algonode/algovnode/internal/config"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/sirupsen/logrus"
)

type defaultHandler struct {
	ucache  *blockcache.UnifiedBlockCache
	cluster *algod.NodeCluster
}

func New(ctx context.Context, cancel context.CancelFunc, cache *blockcache.UnifiedBlockCache, cluster *algod.NodeCluster, cfg config.AlgoVNodeConfig, log *logrus.Logger) {
	e := echo.New()

	e.Use(MakeLogger(log))
	e.Use(middleware.CORS())

	if len(cfg.Algod.Virtual.Token) > 0 {
		e.Use(MakeAuth("X-Indexer-API-Token", []string{cfg.Algod.Virtual.Token}))
	}

	e.GET("/", proxyDefault)

	getctx := func(l net.Listener) context.Context {
		return ctx
	}
	s := &http.Server{
		Addr:           cfg.Algod.Virtual.Listen,
		ReadTimeout:    time.Second * 15,
		WriteTimeout:   time.Second * 15,
		MaxHeaderBytes: 1 << 20,
		BaseContext:    getctx,
	}

	go func() {
		log.Fatal(e.StartServer(s))
		cancel()
	}()

}
