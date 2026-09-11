// Package dispatch is the boundary between the room event loops and anything
// slow. Room.run() is a single goroutine serving every broadcast in its room, so
// an inline database write or HTTP call there stalls the whole room. Everything
// here is enqueue-and-return: bounded, non-blocking, and unable to report an
// error back to the caller, so blocking code cannot creep in later.
package dispatch

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"realtime-chat-platform/chat-service/internal/domain"
	"realtime-chat-platform/chat-service/internal/middleware"
)

// Consumer-side interfaces, satisfied by service.PresenceClient and
// service.MessageService, so the workers can be driven by stubs in tests.
type PresenceSender interface {
	SendEvent(ctx context.Context, event domain.PresenceEvent) error
}

type MessageCreator interface {
	Create(room string, userID int64, content string, createdAt time.Time) error
}

type MessageEventSender interface {
	SendMessageEvent(ctx context.Context, event domain.MessageEvent) error
}

const (
	presenceQueueSize = 256
	archiveQueueSize  = 1024
	notifyQueueSize   = 1024

	presenceEventTimeout = 30 * time.Second
	notifyEventTimeout   = 30 * time.Second
)

type presenceEvent struct {
	room   string
	userID int64
	status domain.PresenceStatus
}

type archiveEvent struct {
	eventID   string
	room      string
	userID    int64
	content   string
	createdAt time.Time
}

type Dispatcher struct {
	presenceClient     PresenceSender
	messageService     MessageCreator
	notificationClient MessageEventSender

	presenceQ chan presenceEvent
	archiveQ  chan archiveEvent
	notifyQ   chan archiveEvent

	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewDispatcher starts its workers itself, matching NewHub and NewRoom.
// notificationClient may be nil, in which case messages are archived but no
// message events leave the process.
func NewDispatcher(presenceClient PresenceSender, messageService MessageCreator, notificationClient MessageEventSender) *Dispatcher {
	d := &Dispatcher{
		presenceClient:     presenceClient,
		messageService:     messageService,
		notificationClient: notificationClient,
		presenceQ:          make(chan presenceEvent, presenceQueueSize),
		archiveQ:           make(chan archiveEvent, archiveQueueSize),
		notifyQ:            make(chan archiveEvent, notifyQueueSize),
		stop:               make(chan struct{}),
	}

	d.wg.Add(3)
	go d.runPresenceWorker()
	go d.runArchiveWorker()
	go d.runNotifyWorker()

	return d
}

// Notify implements ws.PresenceNotifier. Dropping an event is safe here in a way
// it is not for Archive: the heartbeat sweep re-asserts the truth every interval,
// so a lost online/offline repairs itself rather than persisting as a ghost.
func (d *Dispatcher) Notify(room string, userID int64, status domain.PresenceStatus) {
	event := presenceEvent{room: room, userID: userID, status: status}

	select {
	case d.presenceQ <- event:
	default:
		slog.Warn("presence event dropped: queue full",
			"service", "chat-service", "room", room, "user_id", userID, "status", status)
	}
}

// Archive implements ws.MessageArchiver. It never blocks and never fails: a full
// queue drops the message and says so in the logs, which is the honest failure
// mode - the alternative is stalling every client in the room behind a slow disk.
//
// One call from the room fans into two queues here: the database write and the
// message event to notification-service (month 4). The room does not know about
// the second - "this message is durable" is its whole statement, and what
// durability entails is this package's business. The two queues are separate so
// a slow notification-service cannot delay a database write, or vice versa.
func (d *Dispatcher) Archive(room string, userID int64, content string, createdAt time.Time) {
	event := archiveEvent{
		eventID:   middleware.GenerateRequestID(),
		room:      room,
		userID:    userID,
		content:   content,
		createdAt: createdAt,
	}

	select {
	case d.archiveQ <- event:
	default:
		slog.Error("chat message dropped: archive queue full",
			"service", "chat-service", "room", room, "user_id", userID)
	}

	if d.notificationClient == nil {
		return
	}

	select {
	case d.notifyQ <- event:
	default:
		// Less severe than a lost archive row: the message is still in the
		// room and (probably) in the database; only the offline fan-out is
		// lost.
		slog.Warn("message event dropped: notify queue full",
			"service", "chat-service", "room", room, "user_id", userID)
	}
}

// Close stops the workers and waits up to timeout for them to drain.
func (d *Dispatcher) Close(timeout time.Duration) {
	d.stopOnce.Do(func() { close(d.stop) })

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		slog.Warn("dispatcher shutdown timed out; queued events dropped", "service", "chat-service")
	}
}

// Exactly one worker, and this one cannot be scaled: the queue is the only thing
// keeping a user's online and offline in order. Two workers could invert a fast
// rejoin and leave someone wrongly offline until the next heartbeat.
func (d *Dispatcher) runPresenceWorker() {
	defer d.wg.Done()

	for {
		select {
		case <-d.stop:
			d.drainPresence()
			return
		case event := <-d.presenceQ:
			d.handlePresence(event)
		}
	}
}

func (d *Dispatcher) drainPresence() {
	for {
		select {
		case event := <-d.presenceQ:
			d.handlePresence(event)
		default:
			return
		}
	}
}

func (d *Dispatcher) handlePresence(event presenceEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), presenceEventTimeout)
	defer cancel()

	err := d.presenceClient.SendEvent(ctx, domain.PresenceEvent{
		UserID: event.userID,
		Room:   event.room,
		Status: event.status,
	})
	if err != nil {
		slog.Error("presence event delivery failed after retries",
			"service", "chat-service",
			"room", event.room,
			"user_id", event.userID,
			"status", event.status,
			"error", err)
	}
}

// One worker, not a pool: a single writer keeps rows inserted in the order they
// were broadcast. Ordering survives a future pool anyway, because created_at is
// stamped at enqueue rather than at insert time.
func (d *Dispatcher) runArchiveWorker() {
	defer d.wg.Done()

	for {
		select {
		case <-d.stop:
			d.drainArchive()
			return
		case event := <-d.archiveQ:
			d.handleArchive(event)
		}
	}
}

// Once stop is closed, select picks randomly between the two ready cases, so a
// plain loop would abandon whatever is still queued. Drain explicitly instead.
func (d *Dispatcher) drainArchive() {
	for {
		select {
		case event := <-d.archiveQ:
			d.handleArchive(event)
		default:
			return
		}
	}
}

func (d *Dispatcher) handleArchive(event archiveEvent) {
	err := d.messageService.Create(event.room, event.userID, event.content, event.createdAt)
	if err != nil {
		slog.Error("failed to archive chat message",
			"service", "chat-service", "room", event.room, "user_id", event.userID, "error", err)
	}
}

// Independent of the archive worker, and free to become a pool: message events
// carry their own timestamp and id, so order between them does not matter.
func (d *Dispatcher) runNotifyWorker() {
	defer d.wg.Done()

	for {
		select {
		case <-d.stop:
			d.drainNotify()
			return
		case event := <-d.notifyQ:
			d.handleNotify(event)
		}
	}
}

func (d *Dispatcher) drainNotify() {
	for {
		select {
		case event := <-d.notifyQ:
			d.handleNotify(event)
		default:
			return
		}
	}
}

func (d *Dispatcher) handleNotify(event archiveEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), notifyEventTimeout)
	defer cancel()

	err := d.notificationClient.SendMessageEvent(ctx, domain.MessageEvent{
		EventID:   event.eventID,
		Room:      event.room,
		UserID:    event.userID,
		Content:   event.content,
		CreatedAt: event.createdAt,
	})
	if err != nil {
		slog.Error("message event delivery failed after retries",
			"service", "chat-service", "room", event.room, "event_id", event.eventID, "error", err)
	}
}

// The queues are deliberately never closed. Room goroutines can outlive Close
// (the same lifetime asymmetry commit bf2588d fixed for room.unregister), and a
// send on a closed channel panics. Late enqueues instead fill an unread buffer
// and hit the drop branch above: bounded, no panic, no leaked goroutine.
