package edgecache

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

const testHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// A media-service stand-in that counts how often it is asked for the object.
func origin(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/media/"+testHash {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"`+testHash+`"`)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		_, _ = w.Write([]byte("PNGBYTES"))
	}))
	t.Cleanup(server.Close)

	return server
}

func get(h *Handler, hash string, ifNoneMatch string) *httptest.ResponseRecorder {
	e := echo.New()
	request := httptest.NewRequest(http.MethodGet, "/media/"+hash, nil)
	if ifNoneMatch != "" {
		request.Header.Set("If-None-Match", ifNoneMatch)
	}
	recorder := httptest.NewRecorder()
	c := e.NewContext(request, recorder)
	c.SetParamNames("hash")
	c.SetParamValues(hash)
	_ = h.Serve(c)

	return recorder
}

// The verification the roadmap asks for: the second GET is a hit and the
// origin is not consulted.
func TestSecondGetIsServedFromTheEdge(t *testing.T) {
	var hits atomic.Int32
	server := origin(t, &hits)
	h := NewHandler(New(1<<20, 1<<20), server.URL)

	first := get(h, testHash, "")
	if first.Code != http.StatusOK || first.Header().Get(CacheHeader) != "MISS" {
		t.Fatalf("first GET: status=%d X-Cache=%q, want 200 MISS", first.Code, first.Header().Get(CacheHeader))
	}

	second := get(h, testHash, "")
	if second.Code != http.StatusOK || second.Header().Get(CacheHeader) != "HIT" {
		t.Fatalf("second GET: status=%d X-Cache=%q, want 200 HIT", second.Code, second.Header().Get(CacheHeader))
	}
	if second.Body.String() != "PNGBYTES" || second.Header().Get("Content-Type") != "image/png" {
		t.Errorf("cached response differs from origin: body=%q type=%q", second.Body.String(), second.Header().Get("Content-Type"))
	}

	if hits.Load() != 1 {
		t.Errorf("origin was fetched %d times, want exactly 1", hits.Load())
	}
}

// A conditional GET for a cached object never reaches the origin.
func TestConditionalGetAnsweredAtTheEdge(t *testing.T) {
	var hits atomic.Int32
	server := origin(t, &hits)
	h := NewHandler(New(1<<20, 1<<20), server.URL)

	get(h, testHash, "") // fill

	response := get(h, testHash, `"`+testHash+`"`)
	if response.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", response.Code)
	}
	if response.Header().Get(CacheHeader) != "HIT" || response.Header().Get("ETag") != `"`+testHash+`"` {
		t.Errorf("304 headers: X-Cache=%q ETag=%q", response.Header().Get(CacheHeader), response.Header().Get("ETag"))
	}
	if hits.Load() != 1 {
		t.Errorf("origin was fetched %d times, want 1", hits.Load())
	}
}

// A conditional GET on a cold cache must still fill the cache: forwarding the
// condition would yield a bodiless 304 and nothing to keep.
func TestConditionalGetOnMissFillsThenAnswers304(t *testing.T) {
	var hits atomic.Int32
	server := origin(t, &hits)
	cache := New(1<<20, 1<<20)
	h := NewHandler(cache, server.URL)

	response := get(h, testHash, `"`+testHash+`"`)
	if response.Code != http.StatusNotModified || response.Header().Get(CacheHeader) != "MISS" {
		t.Fatalf("status=%d X-Cache=%q, want 304 MISS", response.Code, response.Header().Get(CacheHeader))
	}
	if _, ok := cache.Get(testHash); !ok {
		t.Error("cache was not filled by the conditional miss")
	}
}

func TestNotFoundIsPassedThroughAndNotCached(t *testing.T) {
	var hits atomic.Int32
	server := origin(t, &hits)
	cache := New(1<<20, 1<<20)
	h := NewHandler(cache, server.URL)

	other := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if response := get(h, other, ""); response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
	if cache.Stats().Entries != 0 {
		t.Error("a 404 was cached")
	}
}

func TestOriginDownIs502(t *testing.T) {
	var hits atomic.Int32
	server := origin(t, &hits)
	url := server.URL
	server.Close()

	h := NewHandler(New(1<<20, 1<<20), url)
	if response := get(h, testHash, ""); response.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", response.Code)
	}
}

// N concurrent misses for one hash: one origin fetch, everyone gets the body.
func TestConcurrentMissesCoalesceIntoOneOriginFetch(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(100 * time.Millisecond) // long enough for every client to pile up
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"`+testHash+`"`)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		_, _ = w.Write([]byte("PNGBYTES"))
	}))
	defer server.Close()

	h := NewHandler(New(1<<20, 1<<20), server.URL)

	const clients = 50
	var wg sync.WaitGroup
	var served atomic.Int32
	for range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if response := get(h, testHash, ""); response.Code == http.StatusOK && response.Body.String() == "PNGBYTES" {
				served.Add(1)
			}
		}()
	}
	wg.Wait()

	if hits.Load() != 1 {
		t.Errorf("origin was fetched %d times for %d concurrent clients, want 1", hits.Load(), clients)
	}
	if served.Load() != clients {
		t.Errorf("%d of %d clients got the object", served.Load(), clients)
	}
}

// An object over the per-object cap is served, uncached, not turned into a
// permanent 502 because two services' size limits disagree.
func TestOversizedObjectIsPassedThroughUncached(t *testing.T) {
	big := make([]byte, 2000)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"`+testHash+`"`)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		_, _ = w.Write(big)
	}))
	defer server.Close()

	cache := New(1<<20, 1000)
	h := NewHandler(cache, server.URL)

	response := get(h, testHash, "")
	if response.Code != http.StatusOK || response.Body.Len() != 2000 {
		t.Fatalf("status=%d len=%d, want 200 with the full body", response.Code, response.Body.Len())
	}
	if response.Header().Get(CacheHeader) != "BYPASS" {
		t.Errorf("X-Cache = %q, want BYPASS", response.Header().Get(CacheHeader))
	}
	if response.Header().Get("ETag") != `"`+testHash+`"` {
		t.Errorf("origin headers not forwarded on passthrough: ETag=%q", response.Header().Get("ETag"))
	}
	if cache.Stats().Entries != 0 {
		t.Error("oversized object was cached")
	}
}

// An origin that omits ETag is not cached, and a plain GET never gets a 304.
func TestOriginWithoutETagIsNotCachedAndPlainGetGetsBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		_, _ = w.Write([]byte("PNGBYTES"))
	}))
	defer server.Close()

	cache := New(1<<20, 1<<20)
	h := NewHandler(cache, server.URL)

	for range 2 {
		response := get(h, testHash, "")
		if response.Code != http.StatusOK || response.Body.String() != "PNGBYTES" {
			t.Fatalf("status=%d body=%q, want 200 with body", response.Code, response.Body.String())
		}
	}
	if cache.Stats().Entries != 0 {
		t.Error("an ETag-less response was cached")
	}
}
