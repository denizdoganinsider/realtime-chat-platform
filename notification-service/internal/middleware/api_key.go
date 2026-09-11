package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/labstack/echo/v4"
)

const NotificationKeyHeader = "X-Notification-Key"

// Service-to-service auth for chat-service's message events, mirroring
// presence-service's X-Presence-Key: there is no end user behind an event.
var notificationAPIKey []byte

func InitAPIKey(key string) {
	notificationAPIKey = []byte(key)
}

func APIKeyMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		provided := []byte(c.Request().Header.Get(NotificationKeyHeader))

		if subtle.ConstantTimeCompare(provided, notificationAPIKey) != 1 {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid notification key"})
		}

		return next(c)
	}
}
