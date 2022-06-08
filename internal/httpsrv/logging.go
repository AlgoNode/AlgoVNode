package httpsrv

import (
	"time"

	"github.com/labstack/echo/v4"
	log "github.com/sirupsen/logrus"
)

type loggerMiddleware struct {
	log *log.Logger
	msg string
}

// MakeLogger initializes a logger echo.MiddlewareFunc
func MakeLogger(log *log.Logger, msg string) echo.MiddlewareFunc {
	logger := loggerMiddleware{
		log: log,
		msg: msg,
	}

	return logger.handler
}

// Logger is an echo middleware to add log to the API
func (logger *loggerMiddleware) handler(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) (err error) {
		start := time.Now()

		// Get a reference to the response object.
		res := ctx.Response()
		req := ctx.Request()

		// Propogate the error if the next middleware has a problem
		if err = next(ctx); err != nil {
			ctx.Error(err)
		}

		logger.log.WithFields(log.Fields{
			"ip":     ctx.RealIP(),
			"method": req.Method,
			"status": res.Status,
			"ua":     req.UserAgent(),
			"reqMs":  time.Since(start).Milliseconds(),
			"bytes":  res.Size,
			"path":   req.URL.Path,
		}).Info(logger.msg)

		return
	}
}
