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
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type defaultHandler struct {
	ucache  *blockcache.UnifiedBlockCache
	cluster *algod.NodeCluster
}

func New(ctx context.Context, cancel context.CancelFunc, cache *blockcache.UnifiedBlockCache, cluster *algod.NodeCluster, cfg config.AlgoVNodeConfig, log *logrus.Logger) {
	e := echo.New()

	e.Use(MakeLogger(log))
	e.Use(middleware.CORS())

	if len(cfg.Virtual.Http.Token) > 0 {
		e.Use(MakeAuth("X-Indexer-API-Token", []string{cfg.Virtual.Http.Token}))
	}

	e.GET("/", proxyDefault)

	getctx := func(l net.Listener) context.Context {
		return ctx
	}

	h2s := &http2.Server{
		MaxConcurrentStreams: 250,
		MaxReadFrameSize:     1048576,
		IdleTimeout:          10 * time.Second,
	}

	s := &http.Server{
		Addr:           cfg.Virtual.Http.Listen,
		ReadTimeout:    time.Second * 15,
		WriteTimeout:   time.Second * 15,
		MaxHeaderBytes: 1 << 20,
		BaseContext:    getctx,
		Handler:        h2c.NewHandler(e, h2s),
	}

	go func() {
		if err := s.ListenAndServe(); err != http.ErrServerClosed {
			log.WithError(err).Errorf("Error listening on %s", cfg.Virtual.Http.Listen)
		}
		cancel()
	}()

}
