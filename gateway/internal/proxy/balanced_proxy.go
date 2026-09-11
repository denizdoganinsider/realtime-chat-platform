package proxy

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httputil"

	"realtime-chat-platform/gateway/internal/loadbalancer"
	"realtime-chat-platform/gateway/internal/middleware"

	"github.com/labstack/echo/v4"
)

// NewBalancedProxy is NewServiceProxy over a pool of instances: the strategy
// picks one per request, and the request is then proxied exactly as before.
// One httputil.ReverseProxy per instance, built up front, because the pool's
// membership is fixed for the life of the process.
func NewBalancedProxy(pool *loadbalancer.Pool, strategy loadbalancer.Strategy) echo.HandlerFunc {
	proxies := make(map[*loadbalancer.Backend]*httputil.ReverseProxy, len(pool.Backends()))
	for _, b := range pool.Backends() {
		rp := httputil.NewSingleHostReverseProxy(b.URL)
		rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Error("proxy error", "service", "gateway", "backend", b.String(), "path", r.URL.Path, "error", err)
			w.WriteHeader(http.StatusBadGateway)
		}
		proxies[b] = rp
	}

	return func(c echo.Context) error {
		requestID, _ := c.Get(middleware.RequestIDKey).(string)
		if requestID != "" {
			c.Request().Header.Set(middleware.RequestIDHeader, requestID)
		}

		backend, err := strategy.Pick(c.Request())
		if err != nil {
			return respondUnavailable(c, strategy, requestID, err)
		}

		// backend is the proof the verification asks for: which instance this
		// request_id went to. chat-service logs the same id on its side.
		slog.Info("proxying request",
			"service", "gateway",
			"strategy", strategy.Name(),
			"backend", backend.String(),
			"path", c.Request().URL.Path,
			"request_id", requestID,
		)

		proxies[backend].ServeHTTP(c.Response(), c.Request())
		return nil
	}
}

// 503, not 502: nothing was attempted upstream. The two errors are logged
// apart because they mean different things operationally - "everything is
// down" versus "this one room's instance is down and we are refusing to move
// the room" (see loadbalancer.ConsistentHash).
func respondUnavailable(c echo.Context, strategy loadbalancer.Strategy, requestID string, err error) error {
	var down *loadbalancer.ErrBackendDown
	if errors.As(err, &down) {
		slog.Warn("sticky instance down, failing closed",
			"service", "gateway",
			"strategy", strategy.Name(),
			"backend", down.Backend.String(),
			"key", down.Key,
			"path", c.Request().URL.Path,
			"request_id", requestID,
		)
		return c.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "the instance serving this room is unavailable; retry later",
		})
	}

	slog.Error("no healthy instance",
		"service", "gateway",
		"strategy", strategy.Name(),
		"path", c.Request().URL.Path,
		"request_id", requestID,
		"error", err,
	)
	return c.JSON(http.StatusServiceUnavailable, map[string]string{
		"error": "chat service unavailable",
	})
}
