package middleware

import (
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v4"
)

// TicketRedeemer is the consumer-side view of service.TicketService, declared
// here so middleware does not import service.
type TicketRedeemer interface {
	Redeem(ticket string) (int64, string, bool)
}

// WSTicketMiddleware turns ?ticket=<one-shot> back into an Authorization header
// before the request is proxied on to chat-service.
//
// The asymmetry it exploits: a browser cannot set headers on a WebSocket
// handshake, but the gateway can set them on the outbound proxy request it
// makes. So the long-lived JWT never appears in a URL - only a 30-second
// single-use ticket does, and by the time it could be read out of a log it has
// already been spent.
func WSTicketMiddleware(tickets TicketRedeemer) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			ticket := c.QueryParam("ticket")
			if ticket == "" {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing ticket"})
			}

			userID, role, ok := tickets.Redeem(ticket)
			if !ok {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid or expired ticket"})
			}

			token, err := GenerateToken(userID, role)
			if err != nil {
				slog.Error("failed to mint websocket token", "service", "gateway", "user_id", userID, "error", err)
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to authorize connection"})
			}

			c.Request().Header.Set("Authorization", "Bearer "+token)

			// Strip the spent ticket so nothing downstream - chat-service, its
			// logs, an error page - ever sees a credential in a URL again.
			query := c.Request().URL.Query()
			query.Del("ticket")
			c.Request().URL.RawQuery = query.Encode()

			return next(c)
		}
	}
}
