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

	"realtime-chat-platform/media-service/config"
	"realtime-chat-platform/media-service/internal/controller"
	"realtime-chat-platform/media-service/internal/storage"

	mediaMiddleware "realtime-chat-platform/media-service/internal/middleware"

	"github.com/labstack/echo/v4"
	echoMiddleware "github.com/labstack/echo/v4/middleware"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg := config.LoadConfig()
	mediaMiddleware.InitGatewayKey(cfg.GatewayKey)

	store, err := storage.NewStore(cfg.MediaDir, cfg.MaxUploadByte)
	if err != nil {
		slog.Error("failed to open media store", "dir", cfg.MediaDir, "error", err)
		os.Exit(1)
	}

	mediaController := controller.NewMediaController(store)

	e := echo.New()

	e.Use(mediaMiddleware.RequestIDMiddleware)
	e.Use(mediaMiddleware.LoggerMiddleware)

	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	// Reads are public: the URL is a 256-bit hash, and the point of an
	// avatar is that other people can see it. Writes must come through the
	// gateway. The request-size cap is a little over the file cap so the
	// multipart framing fits; the store enforces the exact file limit.
	e.GET("/media/:hash", mediaController.Get)
	e.POST("/media", mediaController.Upload,
		mediaMiddleware.GatewayAuthMiddleware,
		echoMiddleware.BodyLimit(fmt.Sprintf("%d", cfg.MaxUploadByte+64*1024)),
	)

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
