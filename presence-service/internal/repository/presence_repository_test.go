package repository

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// These exercise the score arithmetic against a real Redis, because the thing
// worth testing is the logical-expiry contract (ZADD with an expiry score, prune
// on read), and a fake that reimplements ZRANGEBYSCORE would only be testing
// itself. Skipped when Redis is not running.
func newTestRepository(t *testing.T, ttl time.Duration) *PresenceRepository {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping: needs a live Redis")
	}

	// DB 15 so a developer's own keys in DB 0 are never flushed. The short dial
	// timeout keeps the skip fast when Redis is not running.
	client := redis.NewClient(&redis.Options{
		Addr:        "localhost:6380",
		DB:          15,
		DialTimeout: 200 * time.Millisecond,
	})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skipf("skipping: Redis unavailable at localhost:6380 (%v)", err)
	}

	if err := client.FlushDB(context.Background()).Err(); err != nil {
		t.Fatalf("failed to flush the test database: %v", err)
	}

	t.Cleanup(func() { client.Close() })

	return NewPresenceRepository(client, ttl)
}

func TestSetOnlineThenListOnline(t *testing.T) {
	r := newTestRepository(t, time.Minute)

	if err := r.SetOnline("general", 2); err != nil {
		t.Fatalf("SetOnline returned error: %v", err)
	}
	if err := r.SetOnline("general", 1); err != nil {
		t.Fatalf("SetOnline returned error: %v", err)
	}

	users, err := r.ListOnline("general")
	if err != nil {
		t.Fatalf("ListOnline returned error: %v", err)
	}

	if !slices.Equal(users, []int64{1, 2}) {
		t.Errorf("ListOnline = %v, want [1 2] (sorted)", users)
	}
}

func TestSetOnlineIsIdempotent(t *testing.T) {
	r := newTestRepository(t, time.Minute)

	for range 3 {
		if err := r.SetOnline("general", 1); err != nil {
			t.Fatalf("SetOnline returned error: %v", err)
		}
	}

	users, err := r.ListOnline("general")
	if err != nil {
		t.Fatalf("ListOnline returned error: %v", err)
	}

	if len(users) != 1 {
		t.Errorf("ListOnline = %v, want a single entry", users)
	}
}

func TestSetOfflineRemovesTheUser(t *testing.T) {
	r := newTestRepository(t, time.Minute)

	if err := r.SetOnline("general", 1); err != nil {
		t.Fatalf("SetOnline returned error: %v", err)
	}
	if err := r.SetOffline("general", 1); err != nil {
		t.Fatalf("SetOffline returned error: %v", err)
	}

	users, err := r.ListOnline("general")
	if err != nil {
		t.Fatalf("ListOnline returned error: %v", err)
	}

	if len(users) != 0 {
		t.Errorf("ListOnline = %v, want empty", users)
	}
}

// The crash case the README's verification turns on: nobody sends an offline,
// and the entry disappears purely because its score fell into the past.
func TestEntriesExpireByScore(t *testing.T) {
	r := newTestRepository(t, 200*time.Millisecond)

	if err := r.SetOnline("general", 1); err != nil {
		t.Fatalf("SetOnline returned error: %v", err)
	}

	users, err := r.ListOnline("general")
	if err != nil {
		t.Fatalf("ListOnline returned error: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("ListOnline = %v, want one entry before expiry", users)
	}

	time.Sleep(300 * time.Millisecond)

	users, err = r.ListOnline("general")
	if err != nil {
		t.Fatalf("ListOnline returned error: %v", err)
	}
	if len(users) != 0 {
		t.Errorf("ListOnline = %v, want empty after the TTL elapsed", users)
	}
}

// What the heartbeat buys: an entry about to expire is pushed back out without
// the client having done anything.
func TestRefreshExtendsExpiry(t *testing.T) {
	r := newTestRepository(t, 300*time.Millisecond)

	if err := r.SetOnline("general", 1); err != nil {
		t.Fatalf("SetOnline returned error: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	if err := r.Refresh("general", []int64{1}); err != nil {
		t.Fatalf("Refresh returned error: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	users, err := r.ListOnline("general")
	if err != nil {
		t.Fatalf("ListOnline returned error: %v", err)
	}
	if len(users) != 1 {
		t.Errorf("ListOnline = %v, want the refreshed entry to still be present", users)
	}
}

func TestRefreshWithNoUsersIsANoop(t *testing.T) {
	r := newTestRepository(t, time.Minute)

	if err := r.Refresh("general", nil); err != nil {
		t.Errorf("Refresh returned error: %v", err)
	}
}

func TestRoomsAreIsolated(t *testing.T) {
	r := newTestRepository(t, time.Minute)

	if err := r.SetOnline("general", 1); err != nil {
		t.Fatalf("SetOnline returned error: %v", err)
	}
	if err := r.SetOnline("random", 2); err != nil {
		t.Fatalf("SetOnline returned error: %v", err)
	}

	users, err := r.ListOnline("general")
	if err != nil {
		t.Fatalf("ListOnline returned error: %v", err)
	}

	if !slices.Equal(users, []int64{1}) {
		t.Errorf("ListOnline(general) = %v, want [1]", users)
	}
}
