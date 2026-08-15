package service

import (
	"errors"
	"fmt"

	"realtime-chat-platform/presence-service/internal/domain"
	"realtime-chat-platform/presence-service/internal/repository"
)

const maxRoomNameLength = 64

// ErrValidation separates "the caller sent nonsense" (400) from "Redis is down"
// (500). The predecessor project collapsed both into a plain errors.New and
// answered 400 for either, which turns an outage into a client-side mystery.
var ErrValidation = errors.New("validation")

type PresenceService struct {
	presenceRepo repository.PresenceRepositoryInterface
}

func NewPresenceService(presenceRepo repository.PresenceRepositoryInterface) *PresenceService {
	return &PresenceService{presenceRepo: presenceRepo}
}

func (s *PresenceService) RecordEvent(event domain.PresenceEvent) error {
	if err := validateRoom(event.Room); err != nil {
		return err
	}

	if event.UserID <= 0 {
		return fmt.Errorf("%w: user_id must be positive", ErrValidation)
	}

	switch event.Status {
	case domain.PresenceStatusOnline:
		return s.presenceRepo.SetOnline(event.Room, event.UserID)
	case domain.PresenceStatusOffline:
		return s.presenceRepo.SetOffline(event.Room, event.UserID)
	default:
		return fmt.Errorf("%w: status must be online or offline", ErrValidation)
	}
}

func (s *PresenceService) Heartbeat(room string, userIDs []int64) error {
	if err := validateRoom(room); err != nil {
		return err
	}

	for _, userID := range userIDs {
		if userID <= 0 {
			return fmt.Errorf("%w: user_id must be positive", ErrValidation)
		}
	}

	return s.presenceRepo.Refresh(room, userIDs)
}

func (s *PresenceService) GetRoom(room string) (*domain.RoomPresence, error) {
	if err := validateRoom(room); err != nil {
		return nil, err
	}

	userIDs, err := s.presenceRepo.ListOnline(room)
	if err != nil {
		return nil, err
	}

	return &domain.RoomPresence{
		Room:  room,
		Users: userIDs,
		Count: len(userIDs),
	}, nil
}

// The room name ends up inside a Redis key (presence:room:<room>), so an
// unvalidated name could collide across the namespace - a room literally called
// "general:extra" would be indistinguishable from a nested key. chat-service
// enforces the same rule at the WebSocket handshake; this is the independent
// re-validation every service does for itself.
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
