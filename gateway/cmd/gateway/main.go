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
	"realtime-chat-platform/gateway/internal/loadbalancer"
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

	// Every chat-service instance the gateway may route to. The pool is the
	// load balancer's view of the world: fixed membership, live health.
	chatPool, err := loadbalancer.NewPool(cfg.ChatServiceURLs)
	if err != nil {
		slog.Error("failed to build chat-service pool", "error", err)
		os.Exit(1)
	}
	chatPool.Start(time.Duration(cfg.LBHealthIntervalSecs) * time.Second)
	defer chatPool.Close()

	// Two strategies for two kinds of request. History is a database read any
	// instance can serve, so it round-robins. /ws must be sticky per room -
	// the Hub is per-process - so it hashes on the room name.
	chatHistoryProxy := proxy.NewBalancedProxy(chatPool, loadbalancer.NewRoundRobin(chatPool))
	chatWSProxy := proxy.NewBalancedProxy(chatPool, loadbalancer.NewConsistentHash(chatPool, loadbalancer.RoomKey))

	presenceProxy, err := proxy.NewServiceProxy(cfg.PresenceServiceURL)
	if err != nil {
		slog.Error("failed to build presence-service proxy", "error", err)
		os.Exit(1)
	}

	e := echo.New()

	e.Use(gatewayMiddleware.RequestIDMiddleware)
	e.Use(gatewayMiddleware.LoggerMiddleware)

	// The gateway's own health also reports what it knows about the pool, so
	// "which instance does the gateway think is down" is one curl away.
	e.GET("/health", func(c echo.Context) error {
		backends := make([]map[string]any, 0, len(chatPool.Backends()))
		for _, b := range chatPool.Backends() {
			backends = append(backends, map[string]any{"backend": b.String(), "healthy": b.Healthy()})
		}
		return c.JSON(http.StatusOK, map[string]any{"status": "ok", "chat_service": backends})
	})

	e.POST("/register", authController.Register)
	e.POST("/login", authController.Login)

	auth := e.Group("")
	auth.Use(gatewayMiddleware.JWTMiddleware)
	auth.GET("/me", authController.Me)
	auth.POST("/ws-ticket", ticketController.Issue)

	// Message history is a database read, so any chat-service instance can
	// answer it: round-robin. The Bearer token is forwarded as-is and
	// chat-service re-validates it independently.
	auth.GET("/rooms/:room/messages", chatHistoryProxy)

	// /ws is not in the auth group: a browser cannot put a Bearer token on a
	// WebSocket handshake. It presents a one-shot ticket instead, which this
	// middleware exchanges for an Authorization header on the proxied request -
	// something the gateway can set even though the browser cannot.
	//
	// Routed by consistent hash on ?room=, so every connection to a room lands
	// on the one instance whose in-memory Hub holds it.
	e.GET("/ws", chatWSProxy, gatewayMiddleware.WSTicketMiddleware(ticketService))

	// The gateway is the only public entry point, so presence-service's
	// end-user read paths are reachable through it and nowhere else. No path
	// rewriting is needed: the target URL carries no path, so /presence/general
	// is joined unchanged, and the caller's Authorization header is forwarded
	// as-is for presence-service to validate independently.
	//
	// /rooms moved here from chat-service in month 3. With N instances, each
	// chat-service only knows the rooms its own Hub holds, so a round-robined
	// /rooms would answer differently on every call. presence-service sees all
	// of them through Redis, which makes it the source of truth for room
	// metadata. chat-service still serves its own /rooms directly on its port -
	// that per-instance view is how the verification proves stickiness.
	auth.GET("/rooms", presenceProxy)
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
