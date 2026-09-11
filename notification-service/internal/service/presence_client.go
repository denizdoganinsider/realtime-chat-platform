package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"realtime-chat-platform/notification-service/internal/middleware"
)

const presenceRequestTimeout = 3 * time.Second

// Named here rather than imported from presence-service: wire contract, not
// shared code.
const presenceKeyHeader = "X-Presence-Key"

// PresenceClient asks presence-service who is online in a room, on the
// service-to-service path (API key), because there is no end user behind a
// fan-out decision.
type PresenceClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewPresenceClient(baseURL string, apiKey string) *PresenceClient {
	return &PresenceClient{
		baseURL:    baseURL,
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: presenceRequestTimeout, Transport: &http.Transport{MaxIdleConnsPerHost: 4}},
	}
}

func (c *PresenceClient) OnlineUsers(ctx context.Context, room string) ([]int64, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/internal/presence/"+room, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set(presenceKeyHeader, c.apiKey)
	request.Header.Set(middleware.RequestIDHeader, middleware.GenerateRequestID())

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
	}()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("presence-service answered status=%d", response.StatusCode)
	}

	var body struct {
		Users []int64 `json:"users"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		return nil, err
	}

	return body.Users, nil
}
