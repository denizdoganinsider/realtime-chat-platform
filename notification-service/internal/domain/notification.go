package domain

import "time"

// Webhook is a user's registered endpoint. Secret is never serialized on reads
// (GET /webhook); it is returned exactly once, from the PUT that minted it, via
// WebhookWithSecret.
type Webhook struct {
	UserID    int64     `json:"user_id"`
	URL       string    `json:"url"`
	Secret    string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type WebhookWithSecret struct {
	Webhook
	Secret string `json:"secret"`
}

type Subscription struct {
	UserID    int64     `json:"user_id"`
	Room      string    `json:"room"`
	CreatedAt time.Time `json:"created_at"`
}

type DeliveryStatus string

const (
	DeliveryStatusPending   DeliveryStatus = "pending"
	DeliveryStatusDelivered DeliveryStatus = "delivered"
	DeliveryStatusFailed    DeliveryStatus = "failed"
)

// Delivery is the audit row for one (event, recipient) pair.
type Delivery struct {
	ID         int64          `json:"id"`
	EventID    string         `json:"event_id"`
	UserID     int64          `json:"user_id"`
	Room       string         `json:"room"`
	URL        string         `json:"url"`
	Status     DeliveryStatus `json:"status"`
	Attempts   int            `json:"attempts"`
	LastStatus *int           `json:"last_status"`
	LastError  *string        `json:"last_error"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

// MessageEvent is the wire contract chat-service POSTs to /events/message. It is
// duplicated in chat-service's own domain package on purpose - shared contract,
// not shared code, the same stance as every other pair of services here.
type MessageEvent struct {
	EventID   string    `json:"event_id"`
	Room      string    `json:"room"`
	UserID    int64     `json:"user_id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// WebhookPayload is what a receiver gets. The signed body is exactly the JSON
// encoding of this, so a receiver verifies the raw bytes before decoding.
type WebhookPayload struct {
	Event     string       `json:"event"`
	EventID   string       `json:"event_id"`
	Recipient int64        `json:"recipient"`
	Data      MessageEvent `json:"data"`
}
