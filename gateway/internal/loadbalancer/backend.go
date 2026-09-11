// Package loadbalancer picks which chat-service instance a request goes to.
//
// Two strategies, because the two kinds of request behind the gateway are not
// the same problem. Stateless HTTP (message history is a database read) can go
// anywhere, so RoundRobin spreads it evenly. WebSocket connections are sticky:
// chat-service's Hub is in-memory and per-process, so two clients in the same
// room MUST land on the same instance or they never see each other's messages.
// ConsistentHash maps a room name to one instance, deterministically, with no
// shared state between the instances.
package loadbalancer

import (
	"net/url"
	"sync/atomic"
)

// Backend is one chat-service instance. Health is an atomic flag flipped by the
// checker and read on every pick, so the hot path never takes a lock.
type Backend struct {
	URL     *url.URL
	healthy atomic.Bool
}

func NewBackend(rawURL string) (*Backend, error) {
	target, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}

	b := &Backend{URL: target}
	// Optimistic until the first check says otherwise: Pool.Start runs that
	// check synchronously before the gateway accepts traffic, so a dead
	// instance is never picked on the strength of this default.
	b.healthy.Store(true)

	return b, nil
}

func (b *Backend) Healthy() bool {
	return b.healthy.Load()
}

func (b *Backend) setHealthy(healthy bool) (changed bool) {
	return b.healthy.Swap(healthy) != healthy
}

// String is the host:port only - what the logs need to prove which instance
// served a request, and nothing more.
func (b *Backend) String() string {
	return b.URL.Host
}
