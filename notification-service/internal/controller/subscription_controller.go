package controller

import (
	"net/http"

	"realtime-chat-platform/notification-service/internal/middleware"
	"realtime-chat-platform/notification-service/internal/service"

	"github.com/labstack/echo/v4"
)

type SubscribeRequest struct {
	Room string `json:"room"`
}

type SubscriptionController struct {
	subscriptionService *service.SubscriptionService
}

func NewSubscriptionController(subscriptionService *service.SubscriptionService) *SubscriptionController {
	return &SubscriptionController{subscriptionService: subscriptionService}
}

func (sc *SubscriptionController) Subscribe(c echo.Context) error {
	userID := c.Get(middleware.UserIDKey).(int64)

	var request SubscribeRequest
	if err := c.Bind(&request); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	if err := sc.subscriptionService.Subscribe(userID, request.Room); err != nil {
		return respondError(c, err, "failed to subscribe")
	}

	return c.JSON(http.StatusCreated, map[string]string{"room": request.Room})
}

func (sc *SubscriptionController) Unsubscribe(c echo.Context) error {
	userID := c.Get(middleware.UserIDKey).(int64)

	if err := sc.subscriptionService.Unsubscribe(userID, c.Param("room")); err != nil {
		return respondError(c, err, "failed to unsubscribe")
	}

	return c.NoContent(http.StatusNoContent)
}

func (sc *SubscriptionController) List(c echo.Context) error {
	userID := c.Get(middleware.UserIDKey).(int64)

	subscriptions, err := sc.subscriptionService.List(userID)
	if err != nil {
		return respondError(c, err, "failed to list subscriptions")
	}

	return c.JSON(http.StatusOK, subscriptions)
}
