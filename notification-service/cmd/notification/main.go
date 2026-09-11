package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"realtime-chat-platform/notification-service/config"
	"realtime-chat-platform/notification-service/internal/controller"
	"realtime-chat-platform/notification-service/internal/dispatch"
	"realtime-chat-platform/notification-service/internal/repository"
	"realtime-chat-platform/notification-service/internal/service"

	notificationMiddleware "realtime-chat-platform/notification-service/internal/middleware"

	"github.com/labstack/echo/v4"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg := config.LoadConfig()

	notificationMiddleware.InitGatewayKey(cfg.GatewayKey)
	notificationMiddleware.InitAPIKey(cfg.NotificationAPIKey)

	db := config.NewDatabase(cfg)
	defer db.Close()

	webhookRepo := repository.NewWebhookRepository(db)
	subscriptionRepo := repository.NewSubscriptionRepository(db)
	deliveryRepo := repository.NewDeliveryRepository(db)

	// Loopback webhook URLs are a development convenience (the README's
	// receiver runs on localhost) and an SSRF vector anywhere else, so they
	// are opt-in.
	allowLoopback := os.Getenv("WEBHOOK_ALLOW_LOOPBACK") == "true"

	webhookService := service.NewWebhookService(webhookRepo, allowLoopback)
	subscriptionService := service.NewSubscriptionService(subscriptionRepo)
	presenceClient := service.NewPresenceClient(cfg.PresenceServiceURL, cfg.PresenceAPIKey)
	fanout := service.NewFanoutService(subscriptionRepo, webhookRepo, deliveryRepo, presenceClient)
	deliverer := service.NewDeliverer(deliveryRepo)

	dispatcher := dispatch.NewDispatcher(fanout, deliverer, cfg.DeliveryWorkers)
	defer dispatcher.Close(10 * time.Second)

	webhookController := controller.NewWebhookController(webhookService)
	subscriptionController := controller.NewSubscriptionController(subscriptionService)
	deliveryController := controller.NewDeliveryController(deliveryRepo)
	eventController := controller.NewEventController(fanout, dispatcher)

	e := echo.New()

	e.Use(notificationMiddleware.RequestIDMiddleware)
	e.Use(notificationMiddleware.LoggerMiddleware)

	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	// Service-to-service: chat-service's message events.
	internal := e.Group("")
	internal.Use(notificationMiddleware.APIKeyMiddleware)
	internal.POST("/events/message", eventController.Message)

	// End-user paths, reachable through the gateway only: it validated the
	// token and forwards the identity under a shared key.
	user := e.Group("")
	user.Use(notificationMiddleware.GatewayAuthMiddleware)
	user.PUT("/webhook", webhookController.Put)
	user.GET("/webhook", webhookController.Get)
	user.DELETE("/webhook", webhookController.Delete)
	user.POST("/subscriptions", subscriptionController.Subscribe)
	user.GET("/subscriptions", subscriptionController.List)
	user.DELETE("/subscriptions/:room", subscriptionController.Unsubscribe)
	user.GET("/deliveries", deliveryController.List)

	go func() {
		if err := e.Start(fmt.Sprintf(":%s", cfg.ServerPort)); err != nil && err != http.ErrServerClosed {
			e.Logger.Fatal(err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := e.Shutdown(ctx); err != nil {
		e.Logger.Fatal(err)
	}
}
