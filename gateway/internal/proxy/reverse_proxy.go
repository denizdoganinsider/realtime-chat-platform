package proxy

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"

	"realtime-chat-platform/gateway/internal/middleware"

	"github.com/labstack/echo/v4"
)

// NewChatServiceProxy builds a reverse proxy in front of chat-service.
// net/http/httputil.ReverseProxy transparently proxies WebSocket upgrade
// requests too: it detects the Connection: Upgrade header, hijacks the
// underlying TCP connection, and pipes bytes both ways - no extra code
// needed for /ws specifically.
func NewChatServiceProxy(targetURL string) (echo.HandlerFunc, error) {
	target, err := url.Parse(targetURL)
	if err != nil {
		return nil, err
	}

	rp := httputil.NewSingleHostReverseProxy(target)

	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		slog.Error("proxy error", "target", targetURL, "path", r.URL.Path, "error", err)
		w.WriteHeader(http.StatusBadGateway)
	}

	return func(c echo.Context) error {
		requestID, _ := c.Get(middleware.RequestIDKey).(string)
		if requestID != "" {
			c.Request().Header.Set(middleware.RequestIDHeader, requestID)
		}

		slog.Info("proxying request",
			"service", "gateway",
			"target", targetURL,
			"path", c.Request().URL.Path,
			"request_id", requestID,
		)

		rp.ServeHTTP(c.Response(), c.Request())
		return nil
	}, nil
}
