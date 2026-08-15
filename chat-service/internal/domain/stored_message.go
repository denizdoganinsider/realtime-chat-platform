package domain

import "time"

// StoredMessage is the persisted row, deliberately not the same type as Message
// (message.go): that one is the WebSocket envelope - a different shape with a
// different lifetime, carrying join/leave/typing frames that are never stored.
type StoredMessage struct {
	ID        int64     `json:"id"`
	RoomID    string    `json:"room_id"`
	UserID    int64     `json:"user_id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}
