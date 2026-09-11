package edgecache

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

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
