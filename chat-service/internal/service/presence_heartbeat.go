package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"realtime-chat-platform/chat-service/internal/ws"
)

const heartbeatRequestTimeout = 10 * time.Second

// Declared here, on the consumer side, so ws never has to import service - the
// dispatcher already sits between them and the dependency must only point one way.
type HubSnapshotter interface {
	Snapshot() []ws.RoomSnapshot
}

// PresenceHeartbeat re-asserts who is connected, once per interval, one request
// per room. That is what makes presence self-healing: a dropped event, a
// presence-service restart, or a user whose second socket closed all repair
// themselves on the next sweep instead of needing an exactly-once delivery
// guarantee nobody wants to build for a chat sidebar.
type PresenceHeartbeat struct {
	presenceClient *PresenceClient
	hub            HubSnapshotter
	interval       time.Duration
	stop           chan struct{}
	stopOnce       sync.Once
}

func NewPresenceHeartbeat(presenceClient *PresenceClient, hub HubSnapshotter, interval time.Duration) *PresenceHeartbeat {
	s := &PresenceHeartbeat{
		presenceClient: presenceClient,
		hub:            hub,
		interval:       interval,
		stop:           make(chan struct{}),
	}

	go s.run()

	return s
}

func (s *PresenceHeartbeat) Close() {
	s.stopOnce.Do(func() { close(s.stop) })
}

func (s *PresenceHeartbeat) run() {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.sweep()
		}
	}
}

func (s *PresenceHeartbeat) sweep() {
	for _, room := range s.hub.Snapshot() {
		if len(room.UserIDs) == 0 {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), heartbeatRequestTimeout)
		err := s.presenceClient.SendHeartbeat(ctx, room.Name, room.UserIDs)
		cancel()

		if err != nil {
			// Not retried on purpose: the next sweep is the retry.
			slog.Warn("presence heartbeat failed",
				"service", "chat-service", "room", room.Name, "error", err)
		}
	}
}
