package controller

import (
	"errors"
	"log/slog"
	"net/http"

	"realtime-chat-platform/presence-service/internal/domain"
	"realtime-chat-platform/presence-service/internal/service"

	"github.com/labstack/echo/v4"
)

type HeartbeatRequest struct {
	Room       string  `json:"room"`
	InstanceID string  `json:"instance_id"`
	UserIDs    []int64 `json:"user_ids"`
}

type PresenceController struct {
	presenceService *service.PresenceService
}

func NewPresenceController(presenceService *service.PresenceService) *PresenceController {
	return &PresenceController{presenceService: presenceService}
}

// RecordEvent handles POST /events - the join/leave event chat-service sends.
// It answers 202: presence is eventually consistent by design, and the caller
// dispatches asynchronously and never reads the body.
func (pc *PresenceController) RecordEvent(c echo.Context) error {
	var event domain.PresenceEvent
	if err := c.Bind(&event); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	if err := pc.presenceService.RecordEvent(event); err != nil {
		return respondError(c, err, "failed to record presence event")
	}

	return c.JSON(http.StatusAccepted, map[string]string{"message": "recorded"})
}

func (pc *PresenceController) Heartbeat(c echo.Context) error {
	var request HeartbeatRequest
	if err := c.Bind(&request); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	if err := pc.presenceService.Heartbeat(request.Room, request.InstanceID, request.UserIDs); err != nil {
		return respondError(c, err, "failed to refresh presence")
	}

	return c.JSON(http.StatusAccepted, map[string]string{"message": "refreshed"})
}

// ListRooms handles GET /rooms: every room with someone online, fleet-wide.
// This used to be answered by chat-service from its in-memory Hub; with several
// instances each Hub only knows its own rooms, and Redis is the one place that
// sees all of them.
func (pc *PresenceController) ListRooms(c echo.Context) error {
	rooms, err := pc.presenceService.ListRooms()
	if err != nil {
		return respondError(c, err, "failed to list rooms")
	}

	return c.JSON(http.StatusOK, rooms)
}

func (pc *PresenceController) GetRoom(c echo.Context) error {
	room := c.Param("room")

	presence, err := pc.presenceService.GetRoom(room)
	if err != nil {
		return respondError(c, err, "failed to fetch presence")
	}

	return c.JSON(http.StatusOK, presence)
}

// A validation error is the caller's fault and its message is safe to echo
// back. Anything else is Redis being unreachable: log it, and answer 500 with a
// generic message so chat-service's retry loop treats it as retryable.
func respondError(c echo.Context, err error, genericMessage string) error {
	if errors.Is(err, service.ErrValidation) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	slog.Error(genericMessage,
		"service", "presence-service",
		"path", c.Request().URL.Path,
		"error", err,
	)

	return c.JSON(http.StatusInternalServerError, map[string]string{"error": genericMessage})
}
