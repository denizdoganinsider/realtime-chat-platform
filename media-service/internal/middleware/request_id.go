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
			b := make([]byte, 16)
			_, _ = rand.Read(b)
			requestID = hex.EncodeToString(b)
		}

		c.Set(RequestIDKey, requestID)
		c.Response().Header().Set(RequestIDHeader, requestID)

		return next(c)
	}
}
