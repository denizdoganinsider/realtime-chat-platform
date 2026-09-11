package loadbalancer

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
)

func TestConsistentHashIsDeterministic(t *testing.T) {
	pool := newTestPool(t, "http://a:1", "http://b:2", "http://c:3")
	s := NewConsistentHash(pool, RoomKey)

	first, err := s.Pick(httptest.NewRequest("GET", "/ws?room=general", nil))
	if err != nil {
		t.Fatalf("Pick returned error: %v", err)
	}

	for range 50 {
		again, err := s.Pick(httptest.NewRequest("GET", "/ws?room=general", nil))
		if err != nil {
			t.Fatalf("Pick returned error: %v", err)
		}
		if again != first {
			t.Fatalf("same room resolved to %s then %s", first, again)
		}
	}
}

// The property that makes /ws work without shared state: the same room on a
// freshly built ring (a gateway restart, a second gateway) lands on the same
// instance, because nothing about the mapping lives in memory.
func TestConsistentHashSurvivesRebuild(t *testing.T) {
	first := NewConsistentHash(newTestPool(t, "http://a:1", "http://b:2"), RoomKey)
	second := NewConsistentHash(newTestPool(t, "http://a:1", "http://b:2"), RoomKey)

	for i := range 200 {
		room := fmt.Sprintf("room-%d", i)
		if first.lookup(room).String() != second.lookup(room).String() {
			t.Fatalf("room %q moved between two identical rings", room)
		}
	}
}

// An omitted room is "general" on chat-service, so it must be "general" here
// too or the two ways of naming the default room would split across instances.
func TestRoomKeyDefaultsToGeneral(t *testing.T) {
	s := NewConsistentHash(newTestPool(t, "http://a:1", "http://b:2", "http://c:3"), RoomKey)

	implicit, _ := s.Pick(httptest.NewRequest("GET", "/ws", nil))
	explicit, _ := s.Pick(httptest.NewRequest("GET", "/ws?room=general", nil))

	if implicit != explicit {
		t.Errorf("/ws resolved to %s but /ws?room=general to %s", implicit, explicit)
	}
}

// Virtual nodes are what keep the split even. Without them a two-instance ring
// is two points on a circle, and the arc between them is whatever the hash
// function happened to produce.
func TestConsistentHashSpreadsRoomsEvenly(t *testing.T) {
	pool := newTestPool(t, "http://a:1", "http://b:2", "http://c:3")
	s := NewConsistentHash(pool, RoomKey)

	const rooms = 30000
	counts := make(map[string]int)
	for i := range rooms {
		counts[s.lookup(fmt.Sprintf("room-%d", i)).String()]++
	}

	// Perfect is 10000 each; allow ±20%.
	for backend, count := range counts {
		if count < 8000 || count > 12000 {
			t.Errorf("%s got %d of %d rooms; expected roughly a third", backend, count, rooms)
		}
	}
}

// The reason to bother with a ring rather than hash % N: adding a fourth
// instance moves only the rooms that now belong to it (about a quarter), not
// the three quarters that hash % N would reshuffle.
func TestConsistentHashMovesFewKeysWhenScalingOut(t *testing.T) {
	three := NewConsistentHash(newTestPool(t, "http://a:1", "http://b:2", "http://c:3"), RoomKey)
	four := NewConsistentHash(newTestPool(t, "http://a:1", "http://b:2", "http://c:3", "http://d:4"), RoomKey)

	const rooms = 30000
	moved := 0
	for i := range rooms {
		room := fmt.Sprintf("room-%d", i)
		if three.lookup(room).String() != four.lookup(room).String() {
			moved++
		}
	}

	// Ideal is 25%. hash % N would move ~75%; anything under 35% proves the ring.
	if moved > rooms*35/100 {
		t.Errorf("%d of %d rooms moved when adding an instance; want about a quarter", moved, rooms)
	}
	if moved < rooms*15/100 {
		t.Errorf("only %d of %d rooms moved; the new instance is not getting its share", moved, rooms)
	}
}

// Fail closed, not reroute: see the ConsistentHash doc comment.
func TestConsistentHashFailsClosedWhenInstanceDown(t *testing.T) {
	pool := newTestPool(t, "http://a:1", "http://b:2")
	s := NewConsistentHash(pool, RoomKey)

	victim := s.lookup("general")
	victim.setHealthy(false)

	_, err := s.Pick(httptest.NewRequest("GET", "/ws?room=general", nil))

	var down *ErrBackendDown
	if !errors.As(err, &down) {
		t.Fatalf("Pick error = %v, want ErrBackendDown", err)
	}
	if down.Backend != victim || down.Key != "general" {
		t.Errorf("ErrBackendDown = %+v, want backend %s for key general", down, victim)
	}

	// Rooms that hash elsewhere are unaffected.
	for i := range 200 {
		room := fmt.Sprintf("room-%d", i)
		if s.lookup(room) == victim {
			continue
		}
		if _, err := s.Pick(httptest.NewRequest("GET", "/ws?room="+room, nil)); err != nil {
			t.Fatalf("room %q on a healthy instance failed: %v", room, err)
		}
	}
}
