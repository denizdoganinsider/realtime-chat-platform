package ws

import (
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	chatMiddleware "realtime-chat-platform/chat-service/internal/middleware"
	"realtime-chat-platform/chat-service/internal/validation"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

const maxMessageSize = 8 * 1024 // 8KB - generous for a chat text message

// NewUpgrader builds the WebSocket upgrader with an Origin allowlist.
//
// A missing Origin header is allowed and a present one must match: cross-site
// WebSocket hijacking is a browser attack, and browsers always send Origin and
// cannot be told not to. Native clients (websocat, mobile, the verification
// script) send none, and rejecting them would buy no security at all.
func NewUpgrader(allowedOrigins []string) websocket.Upgrader {
	return websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true
			}

			return slices.Contains(allowedOrigins, origin)
		},
	}
}

type Handler struct {
	hub      *Hub
	upgrader websocket.Upgrader
}

func NewHandler(hub *Hub, allowedOrigins []string) *Handler {
	return &Handler{
		hub:      hub,
		upgrader: NewUpgrader(allowedOrigins),
	}
}

// Serve handles GET /ws?room=<name>. The token arrives in the Authorization
// header: the client presents a one-shot ticket to the gateway, which redeems it
// and sets the header on the request it proxies here. Browsers cannot set
// headers on a handshake, but the gateway can on its outbound request - so no
// credential ever travels in a URL.
func (h *Handler) Serve(c echo.Context) error {
	roomName := c.QueryParam("room")
	if roomName == "" {
		roomName = "general"
	}

	if err := validation.ValidateRoomName(roomName); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	authHeader := c.Request().Header.Get("Authorization")
	tokenString := strings.TrimPrefix(authHeader, "Bearer ")
	if authHeader == "" || tokenString == authHeader {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing or invalid authorization header"})
	}

	claims, err := chatMiddleware.ValidateToken(tokenString)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": err.Error()})
	}

	conn, err := h.upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return err
	}
	// Without this, gorilla/websocket has no message-size cap: a single
	// oversized frame gets fully buffered in memory before ever reaching
	// the JSON-decode/drop-on-malformed check in Room.run.
	conn.SetReadLimit(maxMessageSize)

	room := h.hub.GetOrCreateRoom(roomName)
	client := NewClient(conn, room, claims.UserID)

	// Bounded, not a bare blocking send: Hub's reaper (see hub.go) can
	// remove an empty room concurrently with a client joining it. That
	// race is vanishingly rare (see reapEmptyRooms), but an unbounded
	// send here would turn it into a permanently leaked goroutine
	// instead of a dropped connection.
	select {
	case room.register <- client:
	case <-time.After(2 * time.Second):
		slog.Error("failed to register client: room unavailable", "room", roomName, "user_id", claims.UserID)
		conn.Close()
		return nil
	}

	go client.writePump()
	go client.readPump()

	return nil
}

func (h *Handler) ListRooms(c echo.Context) error {
	return c.JSON(http.StatusOK, h.hub.ListRooms())
}
