package validation

import (
	"strings"
	"testing"
)

func TestValidateRoomName(t *testing.T) {
	tests := []struct {
		name    string
		room    string
		wantErr bool
	}{
		{name: "plain name", room: "general", wantErr: false},
		{name: "digits, underscore and hyphen", room: "team-42_alpha", wantErr: false},
		{name: "empty", room: "", wantErr: true},
		// A colon would let a room name reach across presence-service's
		// Redis key namespace (presence:room:<room>).
		{name: "redis key injection", room: "general:extra", wantErr: true},
		{name: "wildcard", room: "general*", wantErr: true},
		{name: "space", room: "the lounge", wantErr: true},
		{name: "at the length limit", room: strings.Repeat("a", MaxRoomNameLength), wantErr: false},
		{name: "one over the limit", room: strings.Repeat("a", MaxRoomNameLength+1), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRoomName(tt.room)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateRoomName(%q) error = %v, wantErr = %v", tt.room, err, tt.wantErr)
			}
		})
	}
}
