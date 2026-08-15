package ws

import (
	"sync"
	"time"
)

const reapInterval = 30 * time.Second

// Hub owns all rooms for this chat-service instance. In month 3, this
// is what a Load Balancer will need to be sticky-session-aware about:
// a client must keep talking to the instance holding its Room.
type Hub struct {
	mu       sync.Mutex
	rooms    map[string]*Room
	stop     chan struct{}
	presence PresenceNotifier
	archiver MessageArchiver
}

func NewHub(presence PresenceNotifier, archiver MessageArchiver) *Hub {
	h := &Hub{
		rooms:    make(map[string]*Room),
		stop:     make(chan struct{}),
		presence: presence,
		archiver: archiver,
	}

	go h.reapEmptyRooms()

	return h
}

// Close stops the reaper goroutine. Rooms a client-supplied "room" query
// param (see ws.Handler.Serve) is otherwise unbounded, so without
// reaping, every distinct room name ever requested leaks one goroutine
// and one map entry for the life of the process.
func (h *Hub) Close() {
	close(h.stop)
}

func (h *Hub) reapEmptyRooms() {
	ticker := time.NewTicker(reapInterval)
	defer ticker.Stop()

	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
			// Holding h.mu for the whole scan means GetOrCreateRoom can
			// never observe a room mid-removal: either it still exists
			// in the map (join succeeds against the live room) or it's
			// already gone (join creates a fresh one).
			h.mu.Lock()
			for name, room := range h.rooms {
				if room.ClientCount() == 0 {
					delete(h.rooms, name)
					room.shutdown()
				}
			}
			h.mu.Unlock()
		}
	}
}

func (h *Hub) GetOrCreateRoom(name string) *Room {
	h.mu.Lock()
	defer h.mu.Unlock()

	room, ok := h.rooms[name]
	if !ok {
		room = NewRoom(name, h.presence, h.archiver)
		h.rooms[name] = room
	}

	return room
}

// RoomSnapshot is who is connected right now, per room. It is what the presence
// heartbeat re-asserts to presence-service each interval, and what a graceful
// shutdown walks to mark everyone offline before the process exits.
type RoomSnapshot struct {
	Name    string
	UserIDs []int64
}

func (h *Hub) Snapshot() []RoomSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()

	snapshot := make([]RoomSnapshot, 0, len(h.rooms))
	for name, room := range h.rooms {
		snapshot = append(snapshot, RoomSnapshot{Name: name, UserIDs: room.UserIDs()})
	}

	return snapshot
}

type RoomInfo struct {
	Name    string `json:"name"`
	Clients int    `json:"clients"`
}

func (h *Hub) ListRooms() []RoomInfo {
	h.mu.Lock()
	defer h.mu.Unlock()

	rooms := make([]RoomInfo, 0, len(h.rooms))
	for name, room := range h.rooms {
		rooms = append(rooms, RoomInfo{Name: name, Clients: room.ClientCount()})
	}

	return rooms
}
