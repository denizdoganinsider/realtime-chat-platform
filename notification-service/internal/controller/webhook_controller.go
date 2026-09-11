package controller

import (
	"errors"
	"log/slog"
	"net/http"

	"realtime-chat-platform/notification-service/internal/middleware"
	"realtime-chat-platform/notification-service/internal/repository"
	"realtime-chat-platform/notification-service/internal/service"

	"github.com/labstack/echo/v4"
)

type PutWebhookRequest struct {
	URL string `json:"url"`
}

type WebhookController struct {
	webhookService *service.WebhookService
}

func NewWebhookController(webhookService *service.WebhookService) *WebhookController {
	return &WebhookController{webhookService: webhookService}
}

// Put handles PUT /webhook. The response carries the secret; nothing else does.
func (wc *WebhookController) Put(c echo.Context) error {
	userID, ok := userIDFrom(c)
	if !ok {
		return unauthorized(c)
	}

	var request PutWebhookRequest
	if err := c.Bind(&request); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	webhook, err := wc.webhookService.Put(userID, request.URL)
	if err != nil {
		return respondError(c, err, "failed to register webhook")
	}

	return c.JSON(http.StatusOK, webhook)
}

func (wc *WebhookController) Get(c echo.Context) error {
	userID, ok := userIDFrom(c)
	if !ok {
		return unauthorized(c)
	}

	webhook, err := wc.webhookService.Get(userID)
	if errors.Is(err, repository.ErrNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "no webhook registered"})
	}
	if err != nil {
		return respondError(c, err, "failed to fetch webhook")
	}

	return c.JSON(http.StatusOK, webhook)
}

func (wc *WebhookController) Delete(c echo.Context) error {
	userID, ok := userIDFrom(c)
	if !ok {
		return unauthorized(c)
	}

	if err := wc.webhookService.Delete(userID); err != nil {
		return respondError(c, err, "failed to delete webhook")
	}

	return c.NoContent(http.StatusNoContent)
}

// userIDFrom reads the identity GatewayAuthMiddleware established. The comma-ok
// form is deliberate: a handler wired outside the gateway-auth group answers
// 401, not a panic (this service registers no Recover middleware).
func userIDFrom(c echo.Context) (int64, bool) {
	userID, ok := c.Get(middleware.UserIDKey).(int64)
	return userID, ok && userID > 0
}

func unauthorized(c echo.Context) error {
	return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
}

// A validation error is the caller's fault and its message is safe to echo
// back. Anything else is the database being unreachable: log it, answer 500.
func respondError(c echo.Context, err error, genericMessage string) error {
	if errors.Is(err, service.ErrValidation) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	slog.Error(genericMessage, "service", "notification-service", "path", c.Request().URL.Path, "error", err)

	return c.JSON(http.StatusInternalServerError, map[string]string{"error": genericMessage})
}
