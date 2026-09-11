package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"time"

	"realtime-chat-platform/chat-service/internal/domain"
	"realtime-chat-platform/chat-service/internal/middleware"
)

const (
	presenceMaxRetries = 3
	// A missed heartbeat repairs itself on the next sweep, so burning seconds of
	// backoff on it is pure waste. Differentiating the retry policy per event
	// type is the part that goes beyond copying a retry loop.
	heartbeatMaxRetries = 0

	presenceRequestTimeout = 5 * time.Second
)

type PresenceClient struct {
	baseURL    string
	apiKey     string
	instanceID string
	httpClient *http.Client
}

// instanceID is stamped on every event and heartbeat, so presence-service can
// tell this process's entries apart from a sibling's - the client owns it
// rather than every caller, because no event can correctly go out without it.
func NewPresenceClient(baseURL string, apiKey string, instanceID string) *PresenceClient {
	return &PresenceClient{
		baseURL:    baseURL,
		apiKey:     apiKey,
		instanceID: instanceID,
		httpClient: &http.Client{
			Timeout: presenceRequestTimeout,
			// Every request goes to the same host, so keeping idle connections
			// around avoids a TCP handshake per presence event.
			Transport: &http.Transport{MaxIdleConnsPerHost: 8},
		},
	}
}

func (s *PresenceClient) SendEvent(ctx context.Context, event domain.PresenceEvent) error {
	event.InstanceID = s.instanceID
	return s.post(ctx, "/events", event, presenceMaxRetries)
}

func (s *PresenceClient) SendHeartbeat(ctx context.Context, room string, userIDs []int64) error {
	body := struct {
		Room       string  `json:"room"`
		InstanceID string  `json:"instance_id"`
		UserIDs    []int64 `json:"user_ids"`
	}{Room: room, InstanceID: s.instanceID, UserIDs: userIDs}

	return s.post(ctx, "/heartbeat", body, heartbeatMaxRetries)
}

// post retries transport failures and 5xx with exponential backoff, and treats
// anything below 500 as final - a 400 will not become valid by being sent again.
func (s *PresenceClient) post(ctx context.Context, path string, body any, maxRetries int) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}

	url := s.baseURL + path
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			if err := sleepWithBackoff(ctx, attempt); err != nil {
				return err
			}
		}

		request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
		if err != nil {
			// A malformed URL will not fix itself on retry.
			return err
		}

		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(presenceKeyHeader, s.apiKey)
		// No inbound request to inherit an id from (a heartbeat has no caller),
		// so mint one to keep both services' logs correlatable.
		request.Header.Set(middleware.RequestIDHeader, middleware.GenerateRequestID())

		response, err := s.httpClient.Do(request)
		if err != nil {
			lastErr = err
			continue
		}

		// Drain before closing, otherwise the keep-alive connection is discarded
		// and every retry pays for a fresh one.
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()

		if response.StatusCode < 500 {
			if response.StatusCode >= 400 {
				return fmt.Errorf("presence-service rejected the request: status=%d", response.StatusCode)
			}
			return nil
		}

		lastErr = fmt.Errorf("presence-service error: status=%d", response.StatusCode)
	}

	return lastErr
}

// Backoff is 1s, 2s, 4s plus up to 50% jitter. Without the jitter, every room's
// retries realign after a presence-service restart and arrive as one thundering
// herd. The sleep is interruptible so shutdown is not held hostage by it.
func sleepWithBackoff(ctx context.Context, attempt int) error {
	base := time.Duration(1<<uint(attempt-1)) * time.Second
	delay := base + rand.N(base/2)

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Named here rather than imported from presence-service: the header name is part
// of the wire contract, and the two services do not share code.
const presenceKeyHeader = "X-Presence-Key"
