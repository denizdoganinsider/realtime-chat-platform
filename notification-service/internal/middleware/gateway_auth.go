package middleware

import (
	"crypto/subtle"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
)

const (
	GatewayKeyHeader = "X-Gateway-Key"
	UserIDHeader     = "X-User-ID"
	UserIDKey        = "user_id"
)

// Month 4's answer to "three copies of jwt.go is where the discipline stops
// being free": the two new services do not parse tokens at all. The gateway
// validates the Bearer token once, at the edge, and forwards the identity in
// X-User-ID together with a shared key proving the request came through it.
// Sharing a key is sharing a config value - the same line the project has
// held since month 1 - where a fourth and fifth jwt.go would be sharing code by
// copy-paste.
//
// The trust this buys is only as good as the network boundary: a caller that can
// reach this port directly AND holds the key can claim any user id. That is
// true of every service-to-service key in this project, and is the reason the
// key is required with no default.
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

		userID, err := strconv.ParseInt(c.Request().Header.Get(UserIDHeader), 10, 64)
		if err != nil || userID <= 0 {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing or invalid user identity"})
		}

		c.Set(UserIDKey, userID)

		return next(c)
	}
}
