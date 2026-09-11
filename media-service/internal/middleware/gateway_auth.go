package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/labstack/echo/v4"
)

const GatewayKeyHeader = "X-Gateway-Key"

// Uploads must come through the gateway, which validated the Bearer token. This
// service records nothing about who uploaded - content is addressed by what it
// is, not who sent it - so the identity header is not read here at all; only
// the proof that the request passed the gateway. See notification-service's
// GatewayAuthMiddleware for the reasoning behind edge auth in month 4.
var gatewayKey []byte

func InitGatewayKey(key string) {
	gatewayKey = []byte(key)
}

func GatewayAuthMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		provided := []byte(c.Request().Header.Get(GatewayKeyHeader))
		if subtle.ConstantTimeCompare(provided, gatewayKey) != 1 {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "requests must come through the gateway"})
		}

		return next(c)
	}
}
