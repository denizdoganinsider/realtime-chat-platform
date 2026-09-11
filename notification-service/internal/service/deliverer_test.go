package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"realtime-chat-platform/notification-service/internal/domain"
)

// recordingDeliveries captures every RecordAttempt so a test can read the
// audit trail the deliverer leaves behind.
type recordingDeliveries struct {
	mu       sync.Mutex
	statuses []domain.DeliveryStatus
	attempts int
	lastErr  *string
}

func (r *recordingDeliveries) Create(*domain.Delivery) error { return nil }
func (r *recordingDeliveries) ListByUserID(int64, int) ([]domain.Delivery, error) {
	return nil, nil
}
func (r *recordingDeliveries) RecordAttempt(id int64, status domain.DeliveryStatus, attempts int, lastStatus *int, lastError *string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statuses = append(r.statuses, status)
	r.attempts = attempts
	r.lastErr = lastError
	return nil
}

func (r *recordingDeliveries) final() (domain.DeliveryStatus, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.statuses[len(r.statuses)-1], r.attempts
}

func newTestDeliverer(repo *recordingDeliveries) *Deliverer {
	d := NewDeliverer(repo, true) // httptest servers live on loopback
	d.now = func() time.Time { return time.Unix(1700000000, 0) }
	d.sleep = func(context.Context, int) error { return nil } // no real backoff in tests
	return d
}

func recipient(url string) Recipient {
	return Recipient{
		Webhook:  domain.Webhook{UserID: 7, URL: url, Secret: "s3cret"},
		Delivery: domain.Delivery{ID: 1},
	}
}

var event = domain.MessageEvent{EventID: "evt1", Room: "general", UserID: 3, Content: "hi", CreatedAt: time.Unix(1700000000, 0)}

// What a receiver sees: a body it can verify with its secret, and the
// timestamp the signature was computed over.
func TestDeliverySignedBodyVerifies(t *testing.T) {
	var got struct {
		body      []byte
		signature string
		timestamp int64
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.body, _ = io.ReadAll(r.Body)
		got.signature = r.Header.Get(SignatureHeader)
		got.timestamp, _ = strconv.ParseInt(r.Header.Get(TimestampHeader), 10, 64)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	repo := &recordingDeliveries{}
	newTestDeliverer(repo).Deliver(context.Background(), recipient(server.URL), event)

	if !Verify("s3cret", got.timestamp, got.body, got.signature) {
		t.Fatal("receiver could not verify the delivered body")
	}

	var payload domain.WebhookPayload
	if err := json.Unmarshal(got.body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Event != "message.created" || payload.Recipient != 7 || payload.Data.EventID != "evt1" {
		t.Errorf("payload = %+v", payload)
	}

	if status, attempts := repo.final(); status != domain.DeliveryStatusDelivered || attempts != 1 {
		t.Errorf("recorded %s after %d attempts, want delivered after 1", status, attempts)
	}
}

func TestRetriesServerErrorsThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	repo := &recordingDeliveries{}
	newTestDeliverer(repo).Deliver(context.Background(), recipient(server.URL), event)

	if status, attempts := repo.final(); status != domain.DeliveryStatusDelivered || attempts != 3 {
		t.Errorf("recorded %s after %d attempts, want delivered after 3", status, attempts)
	}
}

// A 4xx is the receiver's answer, not a transient condition: one attempt.
func TestClientErrorIsFinal(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	repo := &recordingDeliveries{}
	newTestDeliverer(repo).Deliver(context.Background(), recipient(server.URL), event)

	if calls.Load() != 1 {
		t.Errorf("receiver was called %d times, want 1", calls.Load())
	}
	if status, _ := repo.final(); status != domain.DeliveryStatusFailed {
		t.Errorf("recorded %s, want failed", status)
	}
}

func TestGivesUpAfterMaxRetries(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	repo := &recordingDeliveries{}
	newTestDeliverer(repo).Deliver(context.Background(), recipient(server.URL), event)

	if calls.Load() != deliveryMaxRetries+1 {
		t.Errorf("receiver was called %d times, want %d", calls.Load(), deliveryMaxRetries+1)
	}
	if status, attempts := repo.final(); status != domain.DeliveryStatusFailed || attempts != deliveryMaxRetries+1 {
		t.Errorf("recorded %s after %d attempts", status, attempts)
	}
	if repo.lastErr == nil || *repo.lastErr == "" {
		t.Error("no last_error recorded for the failure")
	}
}

// Deliver to the URL as registered: a receiver that redirects elsewhere gets
// the redirect status recorded, not a follow-up POST to somewhere new.
func TestRedirectsAreNotFollowed(t *testing.T) {
	var followed atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/hook", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("/elsewhere", func(w http.ResponseWriter, r *http.Request) {
		followed.Store(true)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	repo := &recordingDeliveries{}
	newTestDeliverer(repo).Deliver(context.Background(), recipient(server.URL+"/hook"), event)

	if followed.Load() {
		t.Error("the redirect was followed")
	}
}

// Shutdown mid-backoff ends the delivery now, not after the remaining sleeps.
func TestCancelledContextStopsRetrying(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	repo := &recordingDeliveries{}
	d := NewDeliverer(repo, true)
	d.sleep = func(ctx context.Context, attempt int) error {
		cancel()
		return ctx.Err()
	}

	d.Deliver(ctx, recipient(server.URL), event)

	if calls.Load() != 1 {
		t.Errorf("receiver was called %d times after cancellation, want 1", calls.Load())
	}
	if status, _ := repo.final(); status != domain.DeliveryStatusFailed {
		t.Errorf("recorded %s, want failed", status)
	}
}
