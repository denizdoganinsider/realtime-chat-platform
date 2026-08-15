package ws

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"realtime-chat-platform/chat-service/internal/domain"
)

type broadcastMessage struct {
	from    *Client
	payload []byte
}

// Both of these are fire-and-forget by construction: no error return and no
// blocking call, because run() below is single-threaded and anything that can
// stall in it stalls every broadcast in the room. An HTTP call to
// presence-service can take seconds with retries. See internal/dispatch.
type PresenceNotifier interface {
	Notify(room string, userID int64, status domain.PresenceStatus)
}

type MessageArchiver interface {
	Archive(room string, userID int64, content string, createdAt time.Time)
}

// Room is a single chat room. run() is the only goroutine that mutates
// clients; mu guards it because ClientCount() is read from other
// goroutines (Hub.ListRooms, Hub's reaper) without going through run().
type Room struct {
	name       string
	mu         sync.RWMutex
	clients    map[*Client]bool
	broadcast  chan broadcastMessage
	register   chan *Client
	unregister chan *Client
	done       chan struct{}
	presence   PresenceNotifier
	archiver   MessageArchiver
}

func NewRoom(name string, presence PresenceNotifier, archiver MessageArchiver) *Room {
	r := &Room{
		name:       name,
		clients:    make(map[*Client]bool),
		broadcast:  make(chan broadcastMessage),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		done:       make(chan struct{}),
		presence:   presence,
		archiver:   archiver,
	}

	go r.run()

	return r
}

// shutdown stops run(). Only called by Hub, under Hub.mu, once the room
// has been removed from Hub.rooms - see Hub.reapEmptyRooms.
func (r *Room) shutdown() {
	close(r.done)
}

func (r *Room) run() {
	for {
		select {
		case <-r.done:
			return

		case client := <-r.register:
			r.mu.Lock()
			r.clients[client] = true
			r.mu.Unlock()

			// Online before the join frame, not after: fanning the frame out can
			// evict this very client as a slow consumer, and that eviction
			// reports it offline. Notifying afterwards would order the pair
			// offline-then-online and leave a disconnected user showing as
			// present until its TTL ran out.
			r.presence.Notify(r.name, client.userID, domain.PresenceStatusOnline)
			r.broadcastEvent(domain.MessageTypeJoin, client.userID, "")

		case client := <-r.unregister:
			r.mu.Lock()
			_, ok := r.clients[client]
			if ok {
				delete(r.clients, client)
			}
			r.mu.Unlock()

			if ok {
				close(client.send)
				r.broadcastEvent(domain.MessageTypeLeave, client.userID, "")
				r.presence.Notify(r.name, client.userID, domain.PresenceStatusOffline)
			}

		case msg := <-r.broadcast:
			var incoming domain.Message
			if err := json.Unmarshal(msg.payload, &incoming); err != nil {
				slog.Warn("dropping malformed message", "room", r.name, "error", err)
				continue
			}

			// Only type and content are read from the payload, and type only
			// picks between two server-controlled branches - the sender's id,
			// room and timestamp are always the server's own.
			switch incoming.Type {
			case domain.MessageTypeTyping:
				// Ephemeral by design: a near-keystroke-rate event has no
				// business on an HTTP hop or in a datastore, so it is fanned out
				// in-room and forgotten. Receivers clear the indicator on their
				// own timer. See README, month 2.
				r.broadcastEvent(domain.MessageTypeTyping, msg.from.userID, "")

			default:
				if incoming.Content == "" {
					slog.Warn("dropping empty chat message", "room", r.name, "user_id", msg.from.userID)
					continue
				}

				// Archived here rather than inside broadcastEvent: at this point
				// the payload has passed the decode check and msg.from.userID is
				// the server-trusted id, while broadcastEvent is shared with
				// join/leave/typing, none of which are persisted.
				timestamp := r.broadcastEvent(domain.MessageTypeChat, msg.from.userID, incoming.Content)
				r.archiver.Archive(r.name, msg.from.userID, incoming.Content, timestamp)
			}
		}
	}
}

// Returns the timestamp it stamped so the archived row carries exactly the
// instant every connected client saw.
func (r *Room) broadcastEvent(msgType domain.MessageType, userID int64, content string) time.Time {
	timestamp := time.Now().UTC()

	out := domain.Message{
		Type:      msgType,
		Room:      r.name,
		UserID:    userID,
		Content:   content,
		Timestamp: timestamp,
	}

	encoded, err := json.Marshal(out)
	if err != nil {
		slog.Error("failed to encode message", "room", r.name, "error", err)
		return timestamp
	}

	var evicted []*Client

	r.mu.Lock()
	for client := range r.clients {
		select {
		case client.send <- encoded:
		default:
			// slow consumer - drop it rather than block the room
			close(client.send)
			delete(r.clients, client)
			evicted = append(evicted, client)
		}
	}
	r.mu.Unlock()

	// An evicted client never reaches the unregister case - run() finds ok ==
	// false and skips the leave - so this is the only place its presence can be
	// cleared. We deliberately do not re-broadcast a leave frame here:
	// broadcastEvent calling itself can evict again, and r.mu is not reentrant.
	// The in-room leave frame is cosmetic; the presence flip is not.
	for _, client := range evicted {
		slog.Warn("evicted slow consumer", "room", r.name, "user_id", client.userID)
		r.presence.Notify(r.name, client.userID, domain.PresenceStatusOffline)
	}

	return timestamp
}

// UserIDs deduplicates: clients are keyed by connection, so one user with two
// sockets open on the same room appears twice in the map but is one person as
// far as presence is concerned.
func (r *Room) UserIDs() []int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	seen := make(map[int64]bool, len(r.clients))
	userIDs := make([]int64, 0, len(r.clients))
	for client := range r.clients {
		if seen[client.userID] {
			continue
		}
		seen[client.userID] = true
		userIDs = append(userIDs, client.userID)
	}

	return userIDs
}

func (r *Room) ClientCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.clients)
}
