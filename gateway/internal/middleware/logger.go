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

		duration := time.Since(start)
		status := c.Response().Status

		requestID, _ := c.Get(RequestIDKey).(string)

		attrs := []slog.Attr{
			slog.String("service", "gateway"),
			slog.String("method", c.Request().Method),
			slog.String("path", c.Request().URL.Path),
			slog.Int("status", status),
			slog.String("duration", duration.String()),
			slog.String("ip", c.RealIP()),
			slog.String("request_id", requestID),
			slog.String("user_agent", c.Request().UserAgent()),
		}

		if status >= 500 {
			slog.LogAttrs(c.Request().Context(), slog.LevelError, "request", attrs...)
		} else {
			slog.LogAttrs(c.Request().Context(), slog.LevelInfo, "request", attrs...)
		}

		return err
	}
}
