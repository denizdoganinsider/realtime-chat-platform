package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"realtime-chat-platform/gateway/internal/loadbalancer"

	"github.com/labstack/echo/v4"
)

// A backend that answers /health with 200 and everything else with its own
// name, so a test can see which instance a request reached.
func namedBackend(t *testing.T, name string) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("X-Backend", name)
		w.Header().Set("X-Seen-Request-ID", r.Header.Get("X-Request-ID"))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	return server
}

func serve(handler echo.HandlerFunc, target string) *httptest.ResponseRecorder {
	e := echo.New()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	recorder := httptest.NewRecorder()
	c := e.NewContext(request, recorder)
	c.Set("request_id", "req-123")
	_ = handler(c)

	return recorder
}

func TestBalancedProxyRoundRobinAlternates(t *testing.T) {
	a := namedBackend(t, "a")
	b := namedBackend(t, "b")

	pool, err := loadbalancer.NewPool([]string{a.URL, b.URL})
	if err != nil {
		t.Fatal(err)
	}
	pool.Start(time.Hour)
	defer pool.Close()

	handler := NewBalancedProxy(pool, loadbalancer.NewRoundRobin(pool))

	seen := []string{}
	for range 4 {
		recorder := serve(handler, "/rooms/general/messages")
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", recorder.Code)
		}
		seen = append(seen, recorder.Header().Get("X-Backend"))
	}

	if seen[0] == seen[1] || seen[0] != seen[2] || seen[1] != seen[3] {
		t.Errorf("round-robin order = %v, want alternating", seen)
	}
}

func TestBalancedProxyForwardsRequestID(t *testing.T) {
	a := namedBackend(t, "a")

	pool, err := loadbalancer.NewPool([]string{a.URL})
	if err != nil {
		t.Fatal(err)
	}
	pool.Start(time.Hour)
	defer pool.Close()

	recorder := serve(NewBalancedProxy(pool, loadbalancer.NewRoundRobin(pool)), "/rooms/general/messages")

	if got := recorder.Header().Get("X-Seen-Request-ID"); got != "req-123" {
		t.Errorf("backend saw request id %q, want req-123", got)
	}
}

func TestBalancedProxyStickyByRoom(t *testing.T) {
	a := namedBackend(t, "a")
	b := namedBackend(t, "b")

	pool, err := loadbalancer.NewPool([]string{a.URL, b.URL})
	if err != nil {
		t.Fatal(err)
	}
	pool.Start(time.Hour)
	defer pool.Close()

	handler := NewBalancedProxy(pool, loadbalancer.NewConsistentHash(pool, loadbalancer.RoomKey))

	first := serve(handler, "/ws?room=general").Header().Get("X-Backend")
	for range 10 {
		if again := serve(handler, "/ws?room=general").Header().Get("X-Backend"); again != first {
			t.Fatalf("room general went to %s then %s", first, again)
		}
	}
}

func TestBalancedProxyAnswers503WhenAllDown(t *testing.T) {
	a := namedBackend(t, "a")
	url := a.URL
	a.Close()

	pool, err := loadbalancer.NewPool([]string{url})
	if err != nil {
		t.Fatal(err)
	}
	pool.Start(time.Hour)
	defer pool.Close()

	recorder := serve(NewBalancedProxy(pool, loadbalancer.NewRoundRobin(pool)), "/rooms/general/messages")

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if body["error"] == "" {
		t.Error("503 body carries no error message")
	}
}

// The sticky miss: only rooms hashed to the dead instance fail.
func TestBalancedProxyFailsClosedForRoomsOnDeadInstance(t *testing.T) {
	a := namedBackend(t, "a")
	b := namedBackend(t, "b")

	pool, err := loadbalancer.NewPool([]string{a.URL, b.URL})
	if err != nil {
		t.Fatal(err)
	}
	pool.Start(time.Hour)
	defer pool.Close()

	strategy := loadbalancer.NewConsistentHash(pool, loadbalancer.RoomKey)
	handler := NewBalancedProxy(pool, strategy)

	// Find a room on each instance while both are up.
	roomOn := map[string]string{}
	for i := 0; len(roomOn) < 2 && i < 200; i++ {
		room := "room-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		roomOn[serve(handler, "/ws?room="+room).Header().Get("X-Backend")] = room
	}
	if len(roomOn) != 2 {
		t.Fatalf("could not find a room on each instance: %v", roomOn)
	}

	// Kill "a" and let the checker notice.
	a.Close()
	pool.Check()

	if code := serve(handler, "/ws?room="+roomOn["a"]).Code; code != http.StatusServiceUnavailable {
		t.Errorf("room on dead instance: status = %d, want 503", code)
	}
	if code := serve(handler, "/ws?room="+roomOn["b"]).Code; code != http.StatusOK {
		t.Errorf("room on live instance: status = %d, want 200", code)
	}
}
