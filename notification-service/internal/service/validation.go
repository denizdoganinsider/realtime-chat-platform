package service

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

const maxRoomNameLength = 64

// ErrValidation separates "the caller sent nonsense" (400) from "the database
// is down" (500), as presence-service does.
var ErrValidation = errors.New("validation")

func validateRoom(room string) error {
	if room == "" {
		return fmt.Errorf("%w: room is required", ErrValidation)
	}
	if len(room) > maxRoomNameLength {
		return fmt.Errorf("%w: room must be at most %d characters", ErrValidation, maxRoomNameLength)
	}
	for _, r := range room {
		isAllowed := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-'
		if !isAllowed {
			return fmt.Errorf("%w: room may only contain letters, digits, underscore and hyphen", ErrValidation)
		}
	}
	return nil
}

// validateWebhookURL is stricter than the predecessor's "has a scheme and a
// host". This service will POST to whatever is registered here, from inside
// the network, with a service identity - so a URL pointing at localhost or a
// private range is a request to use notification-service as a proxy into
// things a user cannot reach themselves. Loopback is allowed only when the
// caller opts in (local development runs the receiver on localhost).
func validateWebhookURL(raw string, allowLoopback bool) error {
	if raw == "" {
		return fmt.Errorf("%w: url is required", ErrValidation)
	}
	if len(raw) > 2048 {
		return fmt.Errorf("%w: url must be at most 2048 characters", ErrValidation)
	}

	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%w: url must be absolute", ErrValidation)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%w: url scheme must be http or https", ErrValidation)
	}
	if parsed.User != nil {
		return fmt.Errorf("%w: url must not carry credentials", ErrValidation)
	}

	// The string-level check. It catches literals and the obvious names, and
	// is a courtesy to the caller (a 400 now rather than a failed delivery
	// later). The check that actually holds is in the transport - see
	// newSafeTransport - because what a hostname resolves to is not a
	// property of the string.
	host := parsed.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if forbiddenIP(ip, allowLoopback) {
			return fmt.Errorf("%w: url must not point at a private or local address", ErrValidation)
		}
		return nil
	}

	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		if allowLoopback {
			return nil
		}
		return fmt.Errorf("%w: url must not point at a private or local address", ErrValidation)
	}

	return nil
}
