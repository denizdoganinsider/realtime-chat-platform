package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/labstack/echo/v4"
)

const PresenceKeyHeader = "X-Presence-Key"

// End-user auth (JWT) and service-to-service auth (shared API key) are different
// problems, so they get different middleware. /events and /heartbeat are called
// by chat-service, which has no end user to speak for; the gateway does not proxy
// them at all.
var presenceAPIKey []byte

func InitAPIKey(key string) {
	presenceAPIKey = []byte(key)
}

func APIKeyMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		provided := []byte(c.Request().Header.Get(PresenceKeyHeader))

		// Constant-time: a plain == leaks the key one byte at a time to anyone
		// who can measure the response.
		if subtle.ConstantTimeCompare(provided, presenceAPIKey) != 1 {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid presence key"})
		}

		return next(c)
	}
}
