package controller

import (
	"log/slog"
	"net/http"

	"realtime-chat-platform/gateway/internal/middleware"
	"realtime-chat-platform/gateway/internal/service"

	"github.com/labstack/echo/v4"
)

type TicketResponse struct {
	Ticket    string `json:"ticket"`
	ExpiresIn int    `json:"expires_in"`
}

type TicketController struct {
	ticketService *service.TicketService
}

func NewTicketController(ticketService *service.TicketService) *TicketController {
	return &TicketController{ticketService: ticketService}
}

// Issue handles POST /ws-ticket. Guarded by the same Bearer token as /me: the
// ticket is a delegation of an identity the caller already proved.
func (tc *TicketController) Issue(c echo.Context) error {
	userID, ok := c.Get(middleware.UserIDKey).(int64)
	if !ok {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	}

	role, _ := c.Get(middleware.RoleKey).(string)

	ticket, err := tc.ticketService.Issue(userID, role)
	if err != nil {
		slog.Error("failed to issue websocket ticket", "service", "gateway", "user_id", userID, "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to issue ticket"})
	}

	return c.JSON(http.StatusCreated, TicketResponse{
		Ticket:    ticket,
		ExpiresIn: int(service.TicketTTL.Seconds()),
	})
}
