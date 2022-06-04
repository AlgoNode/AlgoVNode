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

func New(ctx context.Context, cancel context.CancelFunc, cache *blockcache.UnifiedBlockCache, cluster *algod.NodeCluster, cfg config.AlgoVNodeConfig, log *logrus.Entry) *http.Server {
	e := echo.New()

	e.Use(MakeLogger(log.Logger))
	e.Use(middleware.Recover())
	e.Use(middleware.CORS())
	//TODO make this configurable
	//e.Use(middleware.Gzip())

	//TODO Ratelimiting
	// if cfg.Virtual.RateLimit > 0 {
	// 	e.Use(middleware.RateLimiter(middleware.NewRateLimiterMemoryStore(cfg.Virtual.RateLimit)))
	// }

	middlewares := make([]echo.MiddlewareFunc, 0)
	if len(cfg.Virtual.Tokens) > 0 {
		middlewares = append(middlewares, MakeAuth("X-Indexer-API-Token", cfg.Virtual.Tokens))
	}

	api := ServerImplementation{
		log:     log.Logger,
		ucache:  cache,
		cluster: cluster,
	}

	RegisterHandlersAuth(e, &api, middlewares...)
	RegisterHandlersNoAuth(e, &api)

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
		Handler:        e,
	}

	if cfg.Virtual.Http.H2C {
		s.Handler = h2c.NewHandler(e, h2s)
	}

	//TODO:
	//handle TLS

	go func() {
		if err := s.ListenAndServe(); err != http.ErrServerClosed {
			log.WithError(err).Errorf("Error listening on %s", cfg.Virtual.Http.Listen)
		}
		cancel()
	}()

	return s

}
