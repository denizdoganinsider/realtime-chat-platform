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
type PresenceEvent struct {
	UserID int64          `json:"user_id"`
	Room   string         `json:"room"`
	Status PresenceStatus `json:"status"`
}

type RoomPresence struct {
	Room  string  `json:"room"`
	Users []int64 `json:"users"`
	Count int     `json:"count"`
}
