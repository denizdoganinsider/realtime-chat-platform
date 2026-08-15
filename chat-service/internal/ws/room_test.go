package ws

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"realtime-chat-platform/chat-service/internal/domain"
)

type recordedNotify struct {
	room   string
	userID int64
	status domain.PresenceStatus
}

type recordedArchive struct {
	room    string
	userID  int64
	content string
}

type recorder struct {
	mu       sync.Mutex
	notified []recordedNotify
	archived []recordedArchive
}

func (r *recorder) Notify(room string, userID int64, status domain.PresenceStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notified = append(r.notified, recordedNotify{room: room, userID: userID, status: status})
}

func (r *recorder) Archive(room string, userID int64, content string, createdAt time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.archived = append(r.archived, recordedArchive{room: room, userID: userID, content: content})
}

func (r *recorder) notifications() []recordedNotify {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedNotify(nil), r.notified...)
}

func (r *recorder) archives() []recordedArchive {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedArchive(nil), r.archived...)
}

// A client with no pumps running: the room only ever touches send and userID, so
// a real socket is not needed to exercise the event loop.
func newTestClient(room *Room, userID int64, sendBuffer int) *Client {
	return &Client{room: room, userID: userID, send: make(chan []byte, sendBuffer)}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}

	t.Fatal("condition not met within 2s")
}

func readMessage(t *testing.T, client *Client) domain.Message {
	t.Helper()

	select {
	case raw := <-client.send:
		var message domain.Message
		if err := json.Unmarshal(raw, &message); err != nil {
			t.Fatalf("failed to decode broadcast: %v", err)
		}
		return message
	case <-time.After(2 * time.Second):
		t.Fatal("no message delivered within 2s")
		return domain.Message{}
	}
}

func TestJoinAndLeaveReportPresence(t *testing.T) {
	rec := &recorder{}
	room := NewRoom("general", rec, rec)
	defer room.shutdown()

	client := newTestClient(room, 7, 4)
	room.register <- client

	if message := readMessage(t, client); message.Type != domain.MessageTypeJoin {
		t.Errorf("first frame type = %q, want %q", message.Type, domain.MessageTypeJoin)
	}

	waitFor(t, func() bool { return len(rec.notifications()) == 1 })
	if got := rec.notifications()[0]; got.status != domain.PresenceStatusOnline || got.userID != 7 || got.room != "general" {
		t.Errorf("join notification = %+v, want general/7/online", got)
	}

	room.unregister <- client

	waitFor(t, func() bool { return len(rec.notifications()) == 2 })
	if got := rec.notifications()[1]; got.status != domain.PresenceStatusOffline || got.userID != 7 {
		t.Errorf("leave notification = %+v, want 7/offline", got)
	}
}

func TestChatMessageIsBroadcastAndArchived(t *testing.T) {
	rec := &recorder{}
	room := NewRoom("general", rec, rec)
	defer room.shutdown()

	sender := newTestClient(room, 1, 8)
	receiver := newTestClient(room, 2, 8)
	room.register <- sender
	room.register <- receiver

	payload, err := json.Marshal(domain.Message{
		Type: domain.MessageTypeChat,
		// Deliberately wrong: the room must ignore the client's claims about
		// who it is and which room it is in.
		UserID:  9999,
		Room:    "somewhere-else",
		Content: "hello",
	})
	if err != nil {
		t.Fatalf("failed to encode payload: %v", err)
	}

	room.broadcast <- broadcastMessage{from: sender, payload: payload}

	waitFor(t, func() bool { return len(rec.archives()) == 1 })
	got := rec.archives()[0]
	if got.userID != 1 || got.room != "general" || got.content != "hello" {
		t.Errorf("archived = %+v, want general/1/hello", got)
	}

	// The receiver sees only its own join frame - the sender joined before it
	// was registered - so drain one, then read the chat frame.
	readMessage(t, receiver)

	message := readMessage(t, receiver)
	if message.Type != domain.MessageTypeChat || message.UserID != 1 || message.Room != "general" {
		t.Errorf("broadcast = %+v, want chat from user 1 in general", message)
	}
}

func TestTypingIsNeitherArchivedNorNotified(t *testing.T) {
	rec := &recorder{}
	room := NewRoom("general", rec, rec)
	defer room.shutdown()

	sender := newTestClient(room, 1, 8)
	receiver := newTestClient(room, 2, 8)
	room.register <- sender
	room.register <- receiver

	waitFor(t, func() bool { return len(rec.notifications()) == 2 })

	payload, err := json.Marshal(domain.Message{Type: domain.MessageTypeTyping})
	if err != nil {
		t.Fatalf("failed to encode payload: %v", err)
	}

	room.broadcast <- broadcastMessage{from: sender, payload: payload}

	readMessage(t, receiver)

	message := readMessage(t, receiver)
	if message.Type != domain.MessageTypeTyping {
		t.Fatalf("frame type = %q, want %q", message.Type, domain.MessageTypeTyping)
	}

	if archives := rec.archives(); len(archives) != 0 {
		t.Errorf("typing was archived: %+v", archives)
	}
	if notifications := rec.notifications(); len(notifications) != 2 {
		t.Errorf("typing produced presence traffic: %+v", notifications[2:])
	}
}

// An evicted slow consumer never reaches the unregister case, so without the
// notify inside broadcastEvent it would stay online until its TTL expired.
func TestEvictedSlowConsumerIsReportedOffline(t *testing.T) {
	rec := &recorder{}
	room := NewRoom("general", rec, rec)
	defer room.shutdown()

	// Zero buffer: the very first frame the room fans out has nowhere to go.
	slow := newTestClient(room, 3, 0)
	room.register <- slow

	waitFor(t, func() bool { return len(rec.notifications()) == 2 })

	notifications := rec.notifications()
	if notifications[0].status != domain.PresenceStatusOnline {
		t.Errorf("first notification = %+v, want online", notifications[0])
	}
	if notifications[1].status != domain.PresenceStatusOffline || notifications[1].userID != 3 {
		t.Errorf("second notification = %+v, want 3/offline", notifications[1])
	}

	waitFor(t, func() bool { return room.ClientCount() == 0 })
}

func TestUserIDsDeduplicatesConnections(t *testing.T) {
	rec := &recorder{}
	room := NewRoom("general", rec, rec)
	defer room.shutdown()

	room.register <- newTestClient(room, 5, 8)
	room.register <- newTestClient(room, 5, 8)
	room.register <- newTestClient(room, 6, 8)

	waitFor(t, func() bool { return room.ClientCount() == 3 })

	userIDs := room.UserIDs()
	if len(userIDs) != 2 {
		t.Errorf("UserIDs() = %v, want two distinct ids for three connections", userIDs)
	}
}
