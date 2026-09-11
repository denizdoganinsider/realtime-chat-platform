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

	"realtime-chat-platform/chat-service/config"
	"realtime-chat-platform/chat-service/internal/controller"
	"realtime-chat-platform/chat-service/internal/dispatch"
	"realtime-chat-platform/chat-service/internal/domain"
	"realtime-chat-platform/chat-service/internal/repository"
	"realtime-chat-platform/chat-service/internal/service"
	"realtime-chat-platform/chat-service/internal/ws"

	chatMiddleware "realtime-chat-platform/chat-service/internal/middleware"

	"github.com/labstack/echo/v4"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg := config.LoadConfig()
	chatMiddleware.InitJWT(cfg.JWTSecret)

	db := config.NewDatabase(cfg)
	defer db.Close()

	messageRepo := repository.NewMessageRepository(db)
	messageService := service.NewMessageService(messageRepo)
	messageController := controller.NewMessageController(messageService)

	presenceClient := service.NewPresenceClient(cfg.PresenceServiceURL, cfg.PresenceAPIKey, cfg.InstanceID)
	notificationClient := service.NewNotificationClient(cfg.NotificationServiceURL, cfg.NotificationAPIKey)

	// Deferred calls run last-in-first-out, so this reads bottom-up at exit: the
	// hub stops first, then the heartbeat, then the dispatcher drains whatever
	// the rooms queued on their way out.
	// The budget covers the archive drain plus the notify drain, which makes
	// one bounded HTTP call per queued event (see notifyDrainTimeout).
	dispatcher := dispatch.NewDispatcher(presenceClient, messageService, notificationClient)
	defer dispatcher.Close(15 * time.Second)

	hub := ws.NewHub(dispatcher, dispatcher)
	defer hub.Close()

	heartbeat := service.NewPresenceHeartbeat(presenceClient, hub,
		time.Duration(cfg.PresenceHeartbeatSecs)*time.Second)
	defer heartbeat.Close()

	handler := ws.NewHandler(hub, cfg.WSAllowedOrigins, cfg.InstanceID)

	e := echo.New()

	// Request ids only, deliberately no request logger: this service writes one
	// log line per WebSocket connection (see ws.Handler.Serve) and nothing per
	// request. See README.
	e.Use(chatMiddleware.RequestIDMiddleware)
	e.Use(chatMiddleware.InstanceHeaderMiddleware(cfg.InstanceID))

	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	// Per-instance view: only the rooms THIS process's Hub holds. The gateway
	// no longer proxies it (presence-service answers /rooms for the whole
	// fleet); it stays here, reachable on this port directly, because "which
	// instance holds room X" is what the month 3 verification needs to see.
	e.GET("/rooms", handler.ListRooms, chatMiddleware.JWTMiddleware)
	e.GET("/rooms/:room/messages", messageController.ListByRoom, chatMiddleware.JWTMiddleware)
	e.GET("/ws", handler.Serve)

	go func() {
		if err := e.Start(fmt.Sprintf(":%s", cfg.ServerPort)); err != nil && err != http.ErrServerClosed {
			e.Logger.Fatal(err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	// Mark everyone offline before going away. Nothing forces this - the TTL
	// would expire them eventually - but it is the difference the README's
	// verification turns on: a graceful stop clears presence at once, while a
	// kill -9 leaves the TTL as the only thing that can.
	for _, room := range hub.Snapshot() {
		for _, userID := range room.UserIDs {
			dispatcher.Notify(room.Name, userID, domain.PresenceStatusOffline)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := e.Shutdown(ctx); err != nil {
		e.Logger.Fatal(err)
	}
}
