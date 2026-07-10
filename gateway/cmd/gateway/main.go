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

	"realtime-chat-platform/gateway/config"
	"realtime-chat-platform/gateway/internal/controller"
	"realtime-chat-platform/gateway/internal/proxy"
	"realtime-chat-platform/gateway/internal/repository"
	"realtime-chat-platform/gateway/internal/service"

	gatewayMiddleware "realtime-chat-platform/gateway/internal/middleware"

	"github.com/labstack/echo/v4"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg := config.LoadConfig()

	gatewayMiddleware.InitJWT(cfg.JWTSecret)

	db := config.NewDatabase(cfg)
	defer db.Close()

	userRepo := repository.NewUserRepository(db)
	userService := service.NewUserService(userRepo)
	authController := controller.NewAuthController(userService)

	chatProxy, err := proxy.NewChatServiceProxy(cfg.ChatServiceURL)
	if err != nil {
		slog.Error("failed to build chat-service proxy", "error", err)
		os.Exit(1)
	}

	e := echo.New()

	e.Use(gatewayMiddleware.RequestIDMiddleware)
	e.Use(gatewayMiddleware.LoggerMiddleware)

	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	e.POST("/register", authController.Register)
	e.POST("/login", authController.Login)

	auth := e.Group("")
	auth.Use(gatewayMiddleware.JWTMiddleware)
	auth.GET("/me", authController.Me)

	// Reverse-proxied to chat-service. /rooms requires the same Bearer
	// token as /me (chat-service re-validates it independently). /ws
	// carries the JWT as a query param instead, since browser WebSocket
	// clients cannot set custom headers on the handshake - chat-service
	// validates it there.
	auth.GET("/rooms", chatProxy)
	e.GET("/ws", chatProxy)

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
