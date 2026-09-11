package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"realtime-chat-platform/notification-service/internal/domain"
	"realtime-chat-platform/notification-service/internal/repository"
)

// PresenceReader is the consumer-side view of PresenceClient so the fan-out can
// be driven by a stub in tests.
type PresenceReader interface {
	OnlineUsers(ctx context.Context, room string) ([]int64, error)
}

// Recipient is one webhook that should receive one event.
type Recipient struct {
	Webhook  domain.Webhook
	Delivery domain.Delivery
}

// FanoutService turns a message event into the list of webhooks to call: the
// room's subscribers, minus the sender, minus everyone presence-service says is
// online (they saw the message on their socket), keeping only those who have
// registered an endpoint. Each surviving recipient gets a delivery row.
type FanoutService struct {
	subscriptionRepo repository.SubscriptionRepositoryInterface
	webhookRepo      repository.WebhookRepositoryInterface
	deliveryRepo     repository.DeliveryRepositoryInterface
	presence         PresenceReader
}

func NewFanoutService(
	subscriptionRepo repository.SubscriptionRepositoryInterface,
	webhookRepo repository.WebhookRepositoryInterface,
	deliveryRepo repository.DeliveryRepositoryInterface,
	presence PresenceReader,
) *FanoutService {
	return &FanoutService{
		subscriptionRepo: subscriptionRepo,
		webhookRepo:      webhookRepo,
		deliveryRepo:     deliveryRepo,
		presence:         presence,
	}
}

func (s *FanoutService) ValidateEvent(event domain.MessageEvent) error {
	if err := validateRoom(event.Room); err != nil {
		return err
	}
	if event.UserID <= 0 {
		return fmt.Errorf("%w: user_id must be positive", ErrValidation)
	}
	if event.EventID == "" || len(event.EventID) > 32 {
		return fmt.Errorf("%w: event_id is required and at most 32 characters", ErrValidation)
	}
	if event.Content == "" {
		return fmt.Errorf("%w: content is required", ErrValidation)
	}
	return nil
}

func (s *FanoutService) Recipients(ctx context.Context, event domain.MessageEvent) ([]Recipient, error) {
	subscribers, err := s.subscriptionRepo.ListUserIDsByRoom(event.Room)
	if err != nil {
		return nil, fmt.Errorf("listing subscribers: %w", err)
	}

	online, err := s.presence.OnlineUsers(ctx, event.Room)
	if err != nil {
		// Presence unreachable: deliver to everyone rather than no one. A
		// webhook to a user who is online is a duplicate they can ignore; a
		// missing webhook to one who is offline is the feature not working.
		slog.Warn("presence unavailable, notifying all subscribers",
			"service", "notification-service", "room", event.Room, "error", err)
		online = nil
	}

	isOnline := make(map[int64]bool, len(online))
	for _, id := range online {
		isOnline[id] = true
	}

	var candidates []int64
	for _, id := range subscribers {
		if id == event.UserID || isOnline[id] {
			continue
		}
		candidates = append(candidates, id)
	}

	webhooks, err := s.webhookRepo.ListByUserIDs(candidates)
	if err != nil {
		return nil, fmt.Errorf("listing webhooks: %w", err)
	}

	recipients := make([]Recipient, 0, len(webhooks))
	for _, webhook := range webhooks {
		delivery := domain.Delivery{
			EventID: event.EventID,
			UserID:  webhook.UserID,
			Room:    event.Room,
			URL:     webhook.URL,
			Status:  domain.DeliveryStatusPending,
		}
		err := s.deliveryRepo.Create(&delivery)
		if errors.Is(err, repository.ErrDuplicate) {
			// A replayed event: this recipient already has a row, and so
			// already had (or is having) its webhook call. Skip, not resend.
			slog.Info("skipping delivery: already recorded",
				"service", "notification-service", "event_id", event.EventID, "user_id", webhook.UserID)
			continue
		}
		if err != nil {
			// Anything else is the database failing, which must surface as a
			// fan-out error - not vanish as if every recipient were a replay.
			return nil, fmt.Errorf("recording delivery for user %d: %w", webhook.UserID, err)
		}
		recipients = append(recipients, Recipient{Webhook: webhook, Delivery: delivery})
	}

	return recipients, nil
}
