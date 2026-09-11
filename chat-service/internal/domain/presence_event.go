package domain

type PresenceStatus string

const (
	PresenceStatusOnline  PresenceStatus = "online"
	PresenceStatusOffline PresenceStatus = "offline"
)

// PresenceEvent is the body this service POSTs to presence-service /events.
// presence-service declares the identical type in its own domain package: the
// two share the wire contract, not a code library, which is the same stance as
// sharing JWT_SECRET rather than an auth package.
//
// InstanceID names the chat-service process reporting the event. presence-service
// scopes the entry to it, so an offline from one instance cannot erase an online
// from another for the same user (see README, month 3).
type PresenceEvent struct {
	UserID     int64          `json:"user_id"`
	Room       string         `json:"room"`
	Status     PresenceStatus `json:"status"`
	InstanceID string         `json:"instance_id"`
}
