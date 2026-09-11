package controller

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"realtime-chat-platform/media-service/internal/storage"

	"github.com/labstack/echo/v4"
)

type MediaController struct {
	store *storage.Store
}

func NewMediaController(store *storage.Store) *MediaController {
	return &MediaController{store: store}
}

type UploadResponse struct {
	Hash        string `json:"hash"`
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
}

// Upload handles POST /media (multipart, field "file"). The response URL is
// content-addressed: upload the same bytes twice and you get the same URL.
func (mc *MediaController) Upload(c echo.Context) error {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "multipart field 'file' is required"})
	}

	file, err := fileHeader.Open()
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "could not read upload"})
	}
	defer file.Close()

	meta, err := mc.store.Put(file)
	switch {
	case errors.Is(err, storage.ErrTooLarge):
		return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "file too large"})
	case errors.Is(err, storage.ErrUnsupportedType):
		return c.JSON(http.StatusUnsupportedMediaType, map[string]string{"error": err.Error()})
	case err != nil:
		slog.Error("failed to store upload", "service", "media-service", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to store upload"})
	}

	return c.JSON(http.StatusCreated, UploadResponse{
		Hash:        meta.Hash,
		URL:         "/media/" + meta.Hash,
		ContentType: meta.ContentType,
		Size:        meta.Size,
	})
}

// Get handles GET /media/:hash with the three headers a CDN needs from an
// origin:
//
//   - ETag is the content hash, which the URL already is - so a conditional GET
//     can be answered from the URL alone, with no disk read.
//   - Cache-Control: immutable tells every cache between here and the browser
//     that this URL will never serve different bytes, so it need never
//     revalidate. That is true because the address IS the content.
//   - 304 on If-None-Match, so a cache that already holds the bytes sends and
//     receives nothing but headers.
func (mc *MediaController) Get(c echo.Context) error {
	hash := c.Param("hash")
	etag := `"` + hash + `"`

	// Answered before touching the disk: if the client has this ETag, it has
	// these bytes, because there are no other bytes this URL could serve.
	if c.Request().Header.Get("If-None-Match") == etag {
		setCacheHeaders(c, etag)
		return c.NoContent(http.StatusNotModified)
	}

	meta, file, err := mc.store.Open(hash)
	if errors.Is(err, storage.ErrNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
	}
	if err != nil {
		slog.Error("failed to open object", "service", "media-service", "hash", hash, "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to read object"})
	}
	defer file.Close()

	setCacheHeaders(c, etag)
	c.Response().Header().Set("Content-Length", strconv.FormatInt(meta.Size, 10))
	// Never let a browser second-guess the sniffed type into something active.
	c.Response().Header().Set("X-Content-Type-Options", "nosniff")

	return c.Stream(http.StatusOK, meta.ContentType, file)
}

func setCacheHeaders(c echo.Context, etag string) {
	c.Response().Header().Set("ETag", etag)
	c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
}
