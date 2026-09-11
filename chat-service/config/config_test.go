package config

import (
	"strings"
	"testing"
)

// Mirrors presence-service's validateInstanceID cases: anything rejected there
// must be rejected here first, at startup, so a bad id never reaches the wire.
func TestValidInstanceID(t *testing.T) {
	valid := []string{"chat-8001", "chat-svc-0.chat.svc.cluster.local", "A_b-1", strings.Repeat("x", 64)}
	for _, id := range valid {
		if !ValidInstanceID(id) {
			t.Errorf("ValidInstanceID(%q) = false, want true", id)
		}
	}

	invalid := map[string]string{
		"empty":    "",
		"colon":    "chat:8001", // SERVER_PORT=":8001" would default to this
		"slash":    "chat/8001",
		"space":    "chat 8001",
		"too long": strings.Repeat("x", 65),
		"unicode":  "chät",
	}
	for name, id := range invalid {
		if ValidInstanceID(id) {
			t.Errorf("%s: ValidInstanceID(%q) = true, want false", name, id)
		}
	}
}
