package middleware

import "github.com/labstack/echo/v4"

const InstanceHeader = "X-Chat-Instance"

// InstanceHeaderMiddleware stamps every response with this process's id. With
// several instances behind the gateway, a `curl -i` through the proxy shows
// which one answered - and the WebSocket 101 carries it too, since the reverse
// proxy copies upgrade-response headers through.
func InstanceHeaderMiddleware(instanceID string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Response().Header().Set(InstanceHeader, instanceID)
			return next(c)
		}
	}
}
