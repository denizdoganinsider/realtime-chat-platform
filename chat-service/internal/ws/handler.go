package ws

import (
	"log/slog"
	"net/http"
	"time"

	chatMiddleware "realtime-chat-platform/chat-service/internal/middleware"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

const maxMessageSize = 8 * 1024 // 8KB - generous for a chat text message

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// Month 1 scope: any origin allowed. A real deployment would check
	// this against a configured allowlist.
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Handler struct {
	hub *Hub
}

func NewHandler(hub *Hub) *Handler {
	return &Handler{hub: hub}
}

// Serve handles GET /ws?room=<name>&token=<jwt>. Browsers cannot set
// custom headers on the WebSocket handshake, so the JWT travels as a
// query param here instead of the Authorization header used elsewhere.
func (h *Handler) Serve(c echo.Context) error {
	roomName := c.QueryParam("room")
	if roomName == "" {
		roomName = "general"
	}

	token := c.QueryParam("token")
	if token == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing token"})
	}

	claims, err := chatMiddleware.ValidateToken(token)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": err.Error()})
	}

	conn, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
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
