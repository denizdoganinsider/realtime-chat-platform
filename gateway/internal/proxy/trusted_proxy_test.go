package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

// The month 4 trust model is three header operations. This pins all three
// against a request that arrives carrying attacker-chosen values for each.
func TestTrustedProxyOverwritesIdentityAndStripsToken(t *testing.T) {
	var seen http.Header
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer origin.Close()

	handler, err := NewTrustedProxy(origin.URL, "gateway-key")
	if err != nil {
		t.Fatal(err)
	}

	e := echo.New()
	request := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	request.Header.Set("X-User-ID", "999")            // attacker's claim
	request.Header.Set("X-Gateway-Key", "guess")      // attacker's guess
	request.Header.Set("Authorization", "Bearer tok") // the real token
	recorder := httptest.NewRecorder()
	c := e.NewContext(request, recorder)
	c.Set("request_id", "req-1")
	c.Set("user_id", int64(42)) // what JWTMiddleware established

	_ = handler(c)

	if got := seen.Get("X-User-ID"); got != "42" {
		t.Errorf("X-User-ID = %q, want the gateway's 42, not the client's 999", got)
	}
	if got := seen.Get("X-Gateway-Key"); got != "gateway-key" {
		t.Errorf("X-Gateway-Key = %q, want the gateway's own", got)
	}
	if got := seen.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q reached the origin; it must be stripped", got)
	}
	if got := seen.Get("X-Request-ID"); got != "req-1" {
		t.Errorf("X-Request-ID = %q, want req-1", got)
	}
}

// No identity in context (a route wired outside the auth group): the
// client's X-User-ID must still not get through.
func TestTrustedProxyWithoutIdentityForwardsNone(t *testing.T) {
	var seen http.Header
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
	}))
	defer origin.Close()

	handler, _ := NewTrustedProxy(origin.URL, "gateway-key")

	e := echo.New()
	request := httptest.NewRequest(http.MethodPost, "/media", nil)
	request.Header.Set("X-User-ID", "999")
	c := e.NewContext(request, httptest.NewRecorder())

	_ = handler(c)

	if _, present := seen["X-User-Id"]; present {
		t.Errorf("X-User-ID = %q forwarded with no identity in context", seen.Get("X-User-ID"))
	}
}
