package middleware

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/labstack/echo/v4"
)

const RequestIDHeader = "X-Request-ID"
const RequestIDKey = "request_id"

func RequestIDMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		requestID := c.Request().Header.Get(RequestIDHeader)
		if requestID == "" {
			requestID = GenerateRequestID()
		}

		c.Set(RequestIDKey, requestID)
		c.Response().Header().Set(RequestIDHeader, requestID)

		return next(c)
	}
}

// Exported because outbound calls to presence-service mint their own id when
// there is no inbound request to inherit one from - a heartbeat sweep has no
// caller. That keeps the two services' logs correlatable either way.
func GenerateRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
