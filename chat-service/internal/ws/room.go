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
}

func NewRoom(name string) *Room {
	r := &Room{
		name:       name,
		clients:    make(map[*Client]bool),
		broadcast:  make(chan broadcastMessage),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		done:       make(chan struct{}),
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
			}

		case msg := <-r.broadcast:
			var incoming domain.Message
			if err := json.Unmarshal(msg.payload, &incoming); err != nil {
				slog.Warn("dropping malformed message", "room", r.name, "error", err)
				continue
			}

			r.broadcastEvent(domain.MessageTypeChat, msg.from.userID, incoming.Content)
		}
	}
}

func (r *Room) broadcastEvent(msgType domain.MessageType, userID int64, content string) {
	out := domain.Message{
		Type:      msgType,
		Room:      r.name,
		UserID:    userID,
		Content:   content,
		Timestamp: time.Now().UTC(),
	}

	encoded, err := json.Marshal(out)
	if err != nil {
		slog.Error("failed to encode message", "room", r.name, "error", err)
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for client := range r.clients {
		select {
		case client.send <- encoded:
		default:
			// slow consumer - drop it rather than block the room
			close(client.send)
			delete(r.clients, client)
		}
	}
}

func (r *Room) ClientCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.clients)
}
