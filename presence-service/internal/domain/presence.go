package domain

type PresenceStatus string

const (
	PresenceStatusOnline  PresenceStatus = "online"
	PresenceStatusOffline PresenceStatus = "offline"
)

// PresenceEvent is the wire contract chat-service POSTs to /events. It is
// duplicated in chat-service's own domain package on purpose: the two services
// share the contract, not a code library - the same stance as sharing JWT_SECRET
// rather than an auth package.
//
// InstanceID names the chat-service process that sent the event. Presence is
// tracked per (user, instance), so instance A saying "offline" can never erase
// instance B's "online" for the same user.
type PresenceEvent struct {
	UserID     int64          `json:"user_id"`
	Room       string         `json:"room"`
	Status     PresenceStatus `json:"status"`
	InstanceID string         `json:"instance_id"`
}

type RoomPresence struct {
	Room  string  `json:"room"`
	Users []int64 `json:"users"`
	Count int     `json:"count"`
}

// RoomSummary is one entry of GET /rooms: every room with at least one user
// online, across every chat-service instance.
type RoomSummary struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}
