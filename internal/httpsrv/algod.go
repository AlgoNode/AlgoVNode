package httpsrv

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/algonode/algovnode/internal/config"
	"github.com/algonode/algovnode/internal/icluster"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"golang.org/x/time/rate"
)

func NewAlgodProxy(ctx context.Context, cancel context.CancelFunc, cluster *icluster.Cluster, cfg *config.AlgodCfg, log *logrus.Entry) *http.Server {
	e := echo.New()

	e.Use(MakeLogger(log.Logger, "Algod API"))
	e.Use(middleware.Recover())
	e.Use(middleware.CORS())
	//TODO make this configurable
	e.Use(middleware.Gzip())

	//TODO Ratelimiting
	if cfg.RateLimit > 0 {
		e.Use(middleware.RateLimiter(middleware.NewRateLimiterMemoryStore(rate.Limit(cfg.RateLimit))))
	}

	authMiddlewares := make([]echo.MiddlewareFunc, 0)
	if len(cfg.Tokens) > 0 {
		authMiddlewares = append(authMiddlewares, MakeAuth("X-Algo-API-Token", cfg.Tokens))
	}

	api := ServerImplementation{
		log:     log.Logger,
		cluster: cluster,
	}

	RegisterAlgodHandlers(e, &api, authMiddlewares...)

	getctx := func(l net.Listener) context.Context {
		return ctx
	}

	h2s := &http2.Server{
		MaxConcurrentStreams: 250,
		MaxReadFrameSize:     1048576,
		IdleTimeout:          10 * time.Second,
	}

	s := &http.Server{
		Addr:        cfg.Http.Listen,
		ReadTimeout: time.Second * 15,
		// WriteTimeout:   time.Second * 15,
		MaxHeaderBytes: 1 << 20,
		BaseContext:    getctx,
		Handler:        e,
	}

	if cfg.Http.H2C {
		s.Handler = h2c.NewHandler(e, h2s)
	}

	//TODO:
	//handle TLS

	go func() {
		if err := s.ListenAndServe(); err != http.ErrServerClosed {
			log.WithError(err).Errorf("Error listening on %s", cfg.Http.Listen)
		}
		cancel()
	}()

	return s

}
