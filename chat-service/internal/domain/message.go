package domain

import "time"

type MessageType string

const (
	MessageTypeChat  MessageType = "chat"
	MessageTypeJoin  MessageType = "join"
	MessageTypeLeave MessageType = "leave"
	// Typing is the one inbound type a client can select. It is fanned out in
	// the room and nowhere else - never stored, never sent to presence-service.
	MessageTypeTyping MessageType = "typing"
)

type Message struct {
	Type      MessageType `json:"type"`
	Room      string      `json:"room"`
	UserID    int64       `json:"user_id"`
	Content   string      `json:"content,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}
