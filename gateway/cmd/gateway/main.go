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

	ticketService := service.NewTicketService()
	defer ticketService.Close()
	ticketController := controller.NewTicketController(ticketService)

	chatProxy, err := proxy.NewServiceProxy(cfg.ChatServiceURL)
	if err != nil {
		slog.Error("failed to build chat-service proxy", "error", err)
		os.Exit(1)
	}

	presenceProxy, err := proxy.NewServiceProxy(cfg.PresenceServiceURL)
	if err != nil {
		slog.Error("failed to build presence-service proxy", "error", err)
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
	auth.POST("/ws-ticket", ticketController.Issue)

	// Reverse-proxied to chat-service. /rooms requires the same Bearer
	// token as /me (chat-service re-validates it independently).
	auth.GET("/rooms", chatProxy)
	// Echo matches routes exactly, so /rooms does not cover /rooms/:room/messages.
	auth.GET("/rooms/:room/messages", chatProxy)

	// /ws is not in the auth group: a browser cannot put a Bearer token on a
	// WebSocket handshake. It presents a one-shot ticket instead, which this
	// middleware exchanges for an Authorization header on the proxied request -
	// something the gateway can set even though the browser cannot.
	e.GET("/ws", chatProxy, gatewayMiddleware.WSTicketMiddleware(ticketService))

	// The gateway is the only public entry point, so presence-service's
	// end-user read path is reachable through it and nowhere else. No path
	// rewriting is needed: the target URL carries no path, so /presence/general
	// is joined unchanged, and the caller's Authorization header is forwarded
	// as-is for presence-service to validate independently.
	auth.GET("/presence/:room", presenceProxy)

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
