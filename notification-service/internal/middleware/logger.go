package middleware

import (
	"log/slog"
	"time"

	"github.com/labstack/echo/v4"
)

func LoggerMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		start := time.Now()

		err := next(c)

		status := c.Response().Status
		requestID, _ := c.Get(RequestIDKey).(string)

		attrs := []slog.Attr{
			slog.String("service", "notification-service"),
			slog.String("method", c.Request().Method),
			slog.String("path", c.Request().URL.Path),
			slog.Int("status", status),
			slog.String("duration", time.Since(start).String()),
			slog.String("request_id", requestID),
		}

		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		}
		slog.LogAttrs(c.Request().Context(), level, "request", attrs...)

		return err
	}
}
