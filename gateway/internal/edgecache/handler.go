package edgecache

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
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

	// One origin fetch per hash at a time. Without this, N clients asking for
	// a freshly linked avatar before the first fetch lands all miss and all
	// fetch: N origin round trips and N copies of the object in memory for
	// one entry. The first request fetches; the rest wait for its result.
	mu       sync.Mutex
	inflight map[string]*fetchResult
}

// fetchResult is a fetch in progress, or finished: waiters block on done.
type fetchResult struct {
	done   chan struct{}
	entry  *Entry
	status int
	err    error
}

func NewHandler(cache *Cache, originURL string) *Handler {
	return &Handler{
		cache:     cache,
		originURL: strings.TrimRight(originURL, "/"),
		client:    &http.Client{Timeout: originTimeout},
		inflight:  make(map[string]*fetchResult),
	}
}

func (h *Handler) Serve(c echo.Context) error {
	hash := c.Param("hash")
	requestID, _ := c.Get(middleware.RequestIDKey).(string)
	ifNoneMatch := c.Request().Header.Get("If-None-Match")

	if entry, ok := h.cache.Get(hash); ok {
		c.Response().Header().Set(CacheHeader, "HIT")
		h.logServe("edge cache hit", hash, requestID)
		return h.answer(c, entry, ifNoneMatch)
	}

	c.Response().Header().Set(CacheHeader, "MISS")
	h.logServe("edge cache miss", hash, requestID)

	// Fetch the full object even if the client sent If-None-Match: a cache
	// that forwards the conditional would get a 304 with no body and have
	// nothing to keep. Fill first, then answer the client's condition.
	result := h.fetchOnce(c.Request().Context(), hash, requestID)
	if result.err != nil {
		slog.Error("edge cache origin fetch failed", "service", "gateway", "hash", hash, "request_id", requestID, "error", result.err)
		return c.JSON(http.StatusBadGateway, map[string]string{"error": "media origin unavailable"})
	}
	if result.entry == nil {
		if result.status == http.StatusOK {
			// A valid object the cache will not hold (too large, or the origin
			// did not mark it immutable): serve it straight through, uncached,
			// rather than turning a config mismatch into a permanent 502.
			c.Response().Header().Set(CacheHeader, "BYPASS")
			return h.passthrough(c, hash, requestID)
		}
		// 404 and friends: pass the status through, cache nothing.
		return c.NoContent(result.status)
	}

	return h.answer(c, result.entry, ifNoneMatch)
}

// answer writes a cached entry, honouring the client's condition. The
// conditional is answered here, at the edge: the origin never hears about it,
// which is the entire point of the ETag. Empty If-None-Match never matches -
// a plain GET must get the body.
func (h *Handler) answer(c echo.Context, entry *Entry, ifNoneMatch string) error {
	if ifNoneMatch != "" && ifNoneMatch == entry.ETag {
		writeCacheHeaders(c, entry.ETag)
		return c.NoContent(http.StatusNotModified)
	}
	return h.writeEntry(c, entry)
}

// fetchOnce coalesces concurrent misses for the same hash onto one origin
// fetch, and stores the result in the cache before releasing the waiters.
func (h *Handler) fetchOnce(ctx context.Context, hash string, requestID string) *fetchResult {
	h.mu.Lock()
	if result, ok := h.inflight[hash]; ok {
		h.mu.Unlock()
		<-result.done
		return result
	}
	result := &fetchResult{done: make(chan struct{})}
	h.inflight[hash] = result
	h.mu.Unlock()

	// The fetch runs on the first request's context: if that client hangs
	// up mid-fetch the waiters see the error and 502. Rare, and honest.
	result.entry, result.status, result.err = h.fetch(ctx, hash, requestID)
	if result.entry != nil {
		h.cache.Put(hash, result.entry)
	}

	h.mu.Lock()
	delete(h.inflight, hash)
	h.mu.Unlock()
	close(result.done)

	return result
}

// fetch returns a cacheable entry, or (nil, status) when the response must not
// be cached. Only a 200 with an ETag, marked immutable, and within the
// per-object cap is kept: the origin decides what is cacheable, the edge obeys.
func (h *Handler) fetch(ctx context.Context, hash string, requestID string) (*Entry, int, error) {
	response, err := h.originGet(ctx, hash, requestID)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil, response.StatusCode, nil
	}

	etag := response.Header.Get("ETag")
	cacheable := etag != "" &&
		strings.Contains(response.Header.Get("Cache-Control"), "immutable") &&
		response.ContentLength <= h.cache.maxEntry
	if !cacheable {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil, http.StatusOK, nil
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, h.cache.maxEntry+1))
	if err != nil {
		return nil, 0, err
	}
	if int64(len(body)) > h.cache.maxEntry {
		// Content-Length lied. Not cacheable after all; the caller streams it.
		return nil, http.StatusOK, nil
	}

	return &Entry{
		Body:        body,
		ContentType: response.Header.Get("Content-Type"),
		ETag:        etag,
	}, http.StatusOK, nil
}

// passthrough re-fetches an uncacheable object and streams it to the client.
// A second origin round trip, but only for objects the edge has decided not to
// hold - and those are exactly the ones too big to have kept in memory.
func (h *Handler) passthrough(c echo.Context, hash string, requestID string) error {
	response, err := h.originGet(c.Request().Context(), hash, requestID)
	if err != nil {
		slog.Error("edge cache passthrough failed", "service", "gateway", "hash", hash, "request_id", requestID, "error", err)
		return c.JSON(http.StatusBadGateway, map[string]string{"error": "media origin unavailable"})
	}
	defer response.Body.Close()

	for _, name := range []string{"Content-Type", "Content-Length", "ETag", "Cache-Control"} {
		if value := response.Header.Get(name); value != "" {
			c.Response().Header().Set(name, value)
		}
	}
	c.Response().Header().Set("X-Content-Type-Options", "nosniff")

	return c.Stream(response.StatusCode, response.Header.Get("Content-Type"), response.Body)
}

func (h *Handler) originGet(ctx context.Context, hash string, requestID string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.originURL+"/media/"+hash, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set(middleware.RequestIDHeader, requestID)
	return h.client.Do(request)
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
