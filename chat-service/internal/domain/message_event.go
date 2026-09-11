package domain

import "time"

// MessageEvent is the body this service POSTs to notification-service
// /events/message for every chat message it archives. notification-service
// declares the identical type in its own domain package: shared wire contract,
// not shared code.
//
// EventID is minted here, once per message, so notification-service can make a
// retried POST idempotent - the same event arriving twice is one fan-out.
type MessageEvent struct {
	EventID   string    `json:"event_id"`
	Room      string    `json:"room"`
	UserID    int64     `json:"user_id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}
