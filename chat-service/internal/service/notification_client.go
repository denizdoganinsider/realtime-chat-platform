package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"realtime-chat-platform/chat-service/internal/domain"
	"realtime-chat-platform/chat-service/internal/middleware"
)

const (
	notificationMaxRetries     = 3
	notificationRequestTimeout = 5 * time.Second
	// Named here rather than imported from notification-service: wire contract.
	notificationKeyHeader = "X-Notification-Key"
)

// NotificationClient tells notification-service that a message happened. Same
// shape as PresenceClient - and the same reason it is a separate type rather
// than a flag on it: the two are different services with different keys, and a
// change to one's contract must not touch the other.
type NotificationClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewNotificationClient(baseURL string, apiKey string) *NotificationClient {
	return &NotificationClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout:   notificationRequestTimeout,
			Transport: &http.Transport{MaxIdleConnsPerHost: 8},
		},
	}
}

// SendMessageEvent retries transport failures and 5xx with backoff; a 4xx is
// final. notification-service answers 202 as soon as the event is queued.
func (s *NotificationClient) SendMessageEvent(ctx context.Context, event domain.MessageEvent) error {
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}

	url := s.baseURL + "/events/message"
	var lastErr error

	for attempt := 0; attempt <= notificationMaxRetries; attempt++ {
		if attempt > 0 {
			if err := sleepWithBackoff(ctx, attempt); err != nil {
				return err
			}
		}

		request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(notificationKeyHeader, s.apiKey)
		request.Header.Set(middleware.RequestIDHeader, middleware.GenerateRequestID())

		response, err := s.httpClient.Do(request)
		if err != nil {
			lastErr = err
			continue
		}

		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()

		if response.StatusCode < 500 {
			if response.StatusCode >= 400 {
				return fmt.Errorf("notification-service rejected the event: status=%d", response.StatusCode)
			}
			return nil
		}

		lastErr = fmt.Errorf("notification-service error: status=%d", response.StatusCode)
	}

	return lastErr
}
