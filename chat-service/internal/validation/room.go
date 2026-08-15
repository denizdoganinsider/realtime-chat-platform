package validation

import (
	"errors"
	"fmt"
)

const MaxRoomNameLength = 64

// ValidateRoomName guards three separate things at once, which is why it is
// mandatory rather than nice-to-have:
//   - the room name is stored in a VARCHAR(64) column;
//   - it is a client-supplied query param that creates a room (and a goroutine)
//     on first use, so an unbounded set of names is an unbounded set of rooms
//     until the hub's reaper runs;
//   - it ends up inside presence-service's Redis key (presence:room:<room>), so
//     a room literally named "general:extra" could collide across the namespace.
func ValidateRoomName(room string) error {
	if room == "" {
		return errors.New("room is required")
	}

	if len(room) > MaxRoomNameLength {
		return fmt.Errorf("room must be at most %d characters", MaxRoomNameLength)
	}

	for _, r := range room {
		isAllowed := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-'
		if !isAllowed {
			return errors.New("room may only contain letters, digits, underscore and hyphen")
		}
	}

	return nil
}
