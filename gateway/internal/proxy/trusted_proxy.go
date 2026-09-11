package proxy

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"

	"realtime-chat-platform/gateway/internal/middleware"

	"github.com/labstack/echo/v4"
)

const (
	GatewayKeyHeader = "X-Gateway-Key"
	UserIDHeader     = "X-User-ID"
)

// NewTrustedProxy is NewServiceProxy for the month 4 services, which do not
// validate tokens themselves. The gateway has already run JWTMiddleware; this
// forwards the identity it established as X-User-ID, with X-Gateway-Key as
// proof the request passed through here. Both headers are overwritten, never
// merged: whatever a client put in them is discarded.
func NewTrustedProxy(targetURL string, gatewayKey string) (echo.HandlerFunc, error) {
	target, err := url.Parse(targetURL)
	if err != nil {
		return nil, err
	}

	rp := httputil.NewSingleHostReverseProxy(target)
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		slog.Error("proxy error", "service", "gateway", "target", targetURL, "path", r.URL.Path, "error", err)
		w.WriteHeader(http.StatusBadGateway)
	}

	return func(c echo.Context) error {
		requestID, _ := c.Get(middleware.RequestIDKey).(string)
		headers := c.Request().Header

		if requestID != "" {
			headers.Set(middleware.RequestIDHeader, requestID)
		}

		headers.Set(GatewayKeyHeader, gatewayKey)
		headers.Del(UserIDHeader)
		if userID, ok := c.Get(middleware.UserIDKey).(int64); ok {
			headers.Set(UserIDHeader, strconv.FormatInt(userID, 10))
		}
		// The token stays here. Downstream never needed it, and a service
		// that does not receive a credential cannot leak it.
		headers.Del("Authorization")

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
