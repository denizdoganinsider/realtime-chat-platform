package domain

import "time"

type MessageType string

const (
	MessageTypeChat  MessageType = "chat"
	MessageTypeJoin  MessageType = "join"
	MessageTypeLeave MessageType = "leave"
)

type Message struct {
	Type      MessageType `json:"type"`
	Room      string      `json:"room"`
	UserID    int64       `json:"user_id"`
	Content   string      `json:"content,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}
