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

	"realtime-chat-platform/presence-service/config"
	"realtime-chat-platform/presence-service/internal/controller"
	"realtime-chat-platform/presence-service/internal/repository"
	"realtime-chat-platform/presence-service/internal/service"

	presenceMiddleware "realtime-chat-platform/presence-service/internal/middleware"

	"github.com/labstack/echo/v4"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg := config.LoadConfig()

	presenceMiddleware.InitJWT(cfg.JWTSecret)
	presenceMiddleware.InitAPIKey(cfg.PresenceAPIKey)

	redisClient := config.NewRedis(cfg)
	defer redisClient.Close()

	presenceRepo := repository.NewPresenceRepository(redisClient, time.Duration(cfg.PresenceTTLSec)*time.Second)
	presenceService := service.NewPresenceService(presenceRepo)
	presenceController := controller.NewPresenceController(presenceService)

	e := echo.New()

	e.Use(presenceMiddleware.RequestIDMiddleware)
	e.Use(presenceMiddleware.LoggerMiddleware)

	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	// Service-to-service: chat-service authenticates with a shared API key, not
	// an end-user token - there is no end user behind a join/leave event. The
	// gateway does not proxy these, so they are reachable on :8002 only.
	internal := e.Group("")
	internal.Use(presenceMiddleware.APIKeyMiddleware)
	internal.POST("/events", presenceController.RecordEvent)
	internal.POST("/heartbeat", presenceController.Heartbeat)

	// End-user read path: the same Bearer token as /me and /rooms, validated
	// here independently against the shared JWT_SECRET.
	auth := e.Group("")
	auth.Use(presenceMiddleware.JWTMiddleware)
	auth.GET("/presence/:room", presenceController.GetRoom)
	auth.GET("/rooms", presenceController.ListRooms)

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
