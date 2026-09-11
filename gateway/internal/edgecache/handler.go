package edgecache

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"realtime-chat-platform/gateway/internal/middleware"

	"github.com/labstack/echo/v4"
)

const (
	originTimeout = 10 * time.Second
	CacheHeader   = "X-Cache"
)

// Handler serves GET /media/:hash from the cache, going to the origin
// (media-service) only on a miss. It is deliberately not a ReverseProxy: a
// proxy streams the body through, and a cache needs to keep it.
type Handler struct {
	cache     *Cache
	originURL string
	client    *http.Client
}

func NewHandler(cache *Cache, originURL string) *Handler {
	return &Handler{
		cache:     cache,
		originURL: strings.TrimRight(originURL, "/"),
		client:    &http.Client{Timeout: originTimeout},
	}
}

func (h *Handler) Serve(c echo.Context) error {
	hash := c.Param("hash")
	requestID, _ := c.Get(middleware.RequestIDKey).(string)
	ifNoneMatch := c.Request().Header.Get("If-None-Match")

	if entry, ok := h.cache.Get(hash); ok {
		c.Response().Header().Set(CacheHeader, "HIT")
		h.logServe("edge cache hit", hash, requestID)

		// The conditional request is answered here, at the edge: the origin
		// never hears about it, which is the entire point of the ETag.
		if ifNoneMatch == entry.ETag {
			writeCacheHeaders(c, entry.ETag)
			return c.NoContent(http.StatusNotModified)
		}

		return h.writeEntry(c, entry)
	}

	c.Response().Header().Set(CacheHeader, "MISS")
	h.logServe("edge cache miss", hash, requestID)

	// Fetch the full object even if the client sent If-None-Match: a cache
	// that forwards the conditional would get a 304 with no body and have
	// nothing to keep. Fill first, then answer the client's condition.
	entry, status, err := h.fetch(c.Request().Context(), hash, requestID)
	if err != nil {
		slog.Error("edge cache origin fetch failed", "service", "gateway", "hash", hash, "request_id", requestID, "error", err)
		return c.JSON(http.StatusBadGateway, map[string]string{"error": "media origin unavailable"})
	}
	if entry == nil {
		// Not cacheable (404 and friends): pass the status through, cache nothing.
		return c.NoContent(status)
	}

	h.cache.Put(hash, entry)

	if ifNoneMatch == entry.ETag {
		writeCacheHeaders(c, entry.ETag)
		return c.NoContent(http.StatusNotModified)
	}

	return h.writeEntry(c, entry)
}

// fetch returns a cacheable entry, or (nil, status) for a response that must
// be passed through uncached. Only a 200 whose Cache-Control says immutable
// is kept: the origin decides what is cacheable, the edge obeys.
func (h *Handler) fetch(ctx context.Context, hash string, requestID string) (*Entry, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.originURL+"/media/"+hash, nil)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set(middleware.RequestIDHeader, requestID)

	response, err := h.client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil, response.StatusCode, nil
	}
	if !strings.Contains(response.Header.Get("Cache-Control"), "immutable") {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil, http.StatusBadGateway, nil
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, h.cache.maxEntry+1))
	if err != nil {
		return nil, 0, err
	}
	if int64(len(body)) > h.cache.maxEntry {
		// Too big to cache; still too big to have read into memory, so stop
		// here rather than pretend. The size caps on both sides are aligned so
		// this does not happen in practice.
		return nil, http.StatusBadGateway, nil
	}

	return &Entry{
		Body:        body,
		ContentType: response.Header.Get("Content-Type"),
		ETag:        response.Header.Get("ETag"),
	}, http.StatusOK, nil
}

func (h *Handler) writeEntry(c echo.Context, entry *Entry) error {
	writeCacheHeaders(c, entry.ETag)
	c.Response().Header().Set("Content-Length", strconv.Itoa(len(entry.Body)))
	c.Response().Header().Set("X-Content-Type-Options", "nosniff")
	return c.Blob(http.StatusOK, entry.ContentType, entry.Body)
}

func writeCacheHeaders(c echo.Context, etag string) {
	c.Response().Header().Set("ETag", etag)
	c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
}

func (h *Handler) logServe(msg string, hash string, requestID string) {
	slog.Info(msg, "service", "gateway", "hash", hash, "request_id", requestID)
}

func (h *Handler) Stats(c echo.Context) error {
	return c.JSON(http.StatusOK, h.cache.Stats())
}
