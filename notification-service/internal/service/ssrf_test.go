package service

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The hole the string check cannot close: a hostname that resolves to a
// forbidden address. "localhost" is the one such name every machine has.
func TestSafeTransportRefusesHostnameResolvingToLoopback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	_, port, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	client := &http.Client{Transport: newSafeTransport(false)}

	_, err := client.Get("http://localhost:" + port + "/hook")
	if err == nil {
		t.Fatal("a hostname resolving to loopback was dialled")
	}
	if !errors.Is(err, ErrForbiddenDestination) {
		t.Errorf("error = %v, want ErrForbiddenDestination", err)
	}
}

func TestSafeTransportAllowsLoopbackWhenOptedIn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	_, port, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	client := &http.Client{Transport: newSafeTransport(true)}

	response, err := client.Get("http://localhost:" + port + "/hook")
	if err != nil {
		t.Fatalf("loopback with opt-in refused: %v", err)
	}
	response.Body.Close()
}

func TestForbiddenIP(t *testing.T) {
	forbidden := []string{"127.0.0.1", "::1", "10.0.0.5", "172.16.0.1", "192.168.1.1", "169.254.169.254", "0.0.0.0", "fd00::1", "fe80::1", "224.0.0.1"}
	for _, raw := range forbidden {
		if !forbiddenIP(net.ParseIP(raw), false) {
			t.Errorf("forbiddenIP(%s) = false, want true", raw)
		}
	}

	allowed := []string{"93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946"}
	for _, raw := range allowed {
		if forbiddenIP(net.ParseIP(raw), false) {
			t.Errorf("forbiddenIP(%s) = true, want false", raw)
		}
	}

	if forbiddenIP(net.ParseIP("127.0.0.1"), true) {
		t.Error("loopback forbidden despite opt-in")
	}
	if !forbiddenIP(net.ParseIP("10.0.0.5"), true) {
		t.Error("private range allowed under loopback opt-in")
	}
}
