package controller

import (
	"net/http"

	"realtime-chat-platform/notification-service/internal/dispatch"
	"realtime-chat-platform/notification-service/internal/domain"
	"realtime-chat-platform/notification-service/internal/service"

	"github.com/labstack/echo/v4"
)

type EventController struct {
	fanout     *service.FanoutService
	dispatcher *dispatch.Dispatcher
}

func NewEventController(fanout *service.FanoutService, dispatcher *dispatch.Dispatcher) *EventController {
	return &EventController{fanout: fanout, dispatcher: dispatcher}
}

// Message handles POST /events/message from chat-service. 202: the event is
// queued, not delivered - chat-service's dispatcher is waiting on this response
// and must not wait on N receivers.
func (ec *EventController) Message(c echo.Context) error {
	var event domain.MessageEvent
	if err := c.Bind(&event); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	if err := ec.fanout.ValidateEvent(event); err != nil {
		return respondError(c, err, "failed to accept event")
	}

	if !ec.dispatcher.Enqueue(event) {
		// 503 rather than 500: chat-service retries 5xx, and a full queue is
		// exactly the condition a moment's backoff fixes.
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "event queue full"})
	}

	return c.JSON(http.StatusAccepted, map[string]string{"message": "queued"})
}
