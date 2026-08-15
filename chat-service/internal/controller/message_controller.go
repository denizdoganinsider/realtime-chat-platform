package controller

import (
	"log/slog"
	"net/http"
	"strconv"

	"realtime-chat-platform/chat-service/internal/service"
	"realtime-chat-platform/chat-service/internal/validation"

	"github.com/labstack/echo/v4"
)

type MessageController struct {
	messageService *service.MessageService
}

func NewMessageController(messageService *service.MessageService) *MessageController {
	return &MessageController{messageService: messageService}
}

// ListByRoom handles GET /rooms/:room/messages. Broadcast stays in-memory - this
// is the additive history path, not a replacement for it.
func (mc *MessageController) ListByRoom(c echo.Context) error {
	room := c.Param("room")
	if err := validation.ValidateRoomName(room); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	// Zero means "unset": the service owns the defaults and the clamping, so
	// they are not spelled out twice.
	page := 0
	perPage := 0

	if pageParam := c.QueryParam("page"); pageParam != "" {
		parsed, err := strconv.Atoi(pageParam)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid page parameter"})
		}
		page = parsed
	}

	if perPageParam := c.QueryParam("per_page"); perPageParam != "" {
		parsed, err := strconv.Atoi(perPageParam)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid per_page parameter"})
		}
		perPage = parsed
	}

	messages, err := mc.messageService.List(room, page, perPage)
	if err != nil {
		slog.Error("failed to fetch messages", "service", "chat-service", "room", room, "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to fetch messages"})
	}

	return c.JSON(http.StatusOK, messages)
}
