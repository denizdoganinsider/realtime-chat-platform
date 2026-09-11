package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"realtime-chat-platform/notification-service/internal/domain"
)

type stubSubscriptions struct{ byRoom map[string][]int64 }

func (s stubSubscriptions) Create(int64, string) error                        { return nil }
func (s stubSubscriptions) Delete(int64, string) error                        { return nil }
func (s stubSubscriptions) ListByUserID(int64) ([]domain.Subscription, error) { return nil, nil }
func (s stubSubscriptions) ListUserIDsByRoom(room string) ([]int64, error) {
	return s.byRoom[room], nil
}

type stubWebhooks struct{ byUser map[int64]string }

func (s stubWebhooks) Upsert(*domain.Webhook) error               { return nil }
func (s stubWebhooks) GetByUserID(int64) (*domain.Webhook, error) { return nil, nil }
func (s stubWebhooks) DeleteByUserID(int64) error                 { return nil }
func (s stubWebhooks) ListByUserIDs(ids []int64) ([]domain.Webhook, error) {
	var out []domain.Webhook
	for _, id := range ids {
		if url, ok := s.byUser[id]; ok {
			out = append(out, domain.Webhook{UserID: id, URL: url, Secret: "s"})
		}
	}
	return out, nil
}

type stubDeliveries struct {
	created []domain.Delivery
	fail    map[int64]bool
}

func (s *stubDeliveries) Create(d *domain.Delivery) error {
	if s.fail[d.UserID] {
		return errors.New("duplicate")
	}
	d.ID = int64(len(s.created) + 1)
	s.created = append(s.created, *d)
	return nil
}
func (s *stubDeliveries) RecordAttempt(int64, domain.DeliveryStatus, int, *int, *string) error {
	return nil
}
func (s *stubDeliveries) ListByUserID(int64, int) ([]domain.Delivery, error) { return nil, nil }

type stubPresence struct {
	online []int64
	err    error
}

func (s stubPresence) OnlineUsers(context.Context, string) ([]int64, error) {
	return s.online, s.err
}

func userIDs(recipients []Recipient) []int64 {
	ids := make([]int64, 0, len(recipients))
	for _, r := range recipients {
		ids = append(ids, r.Webhook.UserID)
	}
	return ids
}

var msg = domain.MessageEvent{EventID: "evt", Room: "general", UserID: 1, Content: "hi", CreatedAt: time.Now()}

// Subscribers 1..5. 1 is the sender, 2 is online, 4 has no webhook.
func TestRecipientsAreOfflineSubscribersWithWebhooks(t *testing.T) {
	deliveries := &stubDeliveries{}
	f := NewFanoutService(
		stubSubscriptions{byRoom: map[string][]int64{"general": {1, 2, 3, 4, 5}}},
		stubWebhooks{byUser: map[int64]string{1: "u1", 2: "u2", 3: "u3", 5: "u5"}},
		deliveries,
		stubPresence{online: []int64{2}},
	)

	recipients, err := f.Recipients(context.Background(), msg)
	if err != nil {
		t.Fatalf("Recipients returned error: %v", err)
	}

	got := userIDs(recipients)
	if len(got) != 2 || got[0] != 3 || got[1] != 5 {
		t.Errorf("recipients = %v, want [3 5]", got)
	}
	if len(deliveries.created) != 2 {
		t.Errorf("%d delivery rows created, want 2", len(deliveries.created))
	}
}

// Presence down: notify everyone rather than no one.
func TestPresenceOutageNotifiesAllOfflineCandidates(t *testing.T) {
	f := NewFanoutService(
		stubSubscriptions{byRoom: map[string][]int64{"general": {1, 2, 3}}},
		stubWebhooks{byUser: map[int64]string{2: "u2", 3: "u3"}},
		&stubDeliveries{},
		stubPresence{err: errors.New("connection refused")},
	)

	recipients, err := f.Recipients(context.Background(), msg)
	if err != nil {
		t.Fatalf("Recipients returned error: %v", err)
	}
	if got := userIDs(recipients); len(got) != 2 {
		t.Errorf("recipients = %v, want both subscribers", got)
	}
}

// A replayed event: the unique key refuses the second row, and the recipient
// is skipped rather than webhooked twice.
func TestDuplicateDeliveryRowSkipsRecipient(t *testing.T) {
	f := NewFanoutService(
		stubSubscriptions{byRoom: map[string][]int64{"general": {2, 3}}},
		stubWebhooks{byUser: map[int64]string{2: "u2", 3: "u3"}},
		&stubDeliveries{fail: map[int64]bool{2: true}},
		stubPresence{},
	)

	recipients, err := f.Recipients(context.Background(), msg)
	if err != nil {
		t.Fatalf("Recipients returned error: %v", err)
	}
	if got := userIDs(recipients); len(got) != 1 || got[0] != 3 {
		t.Errorf("recipients = %v, want [3]", got)
	}
}

func TestValidateEvent(t *testing.T) {
	f := NewFanoutService(stubSubscriptions{}, stubWebhooks{}, &stubDeliveries{}, stubPresence{})

	bad := []domain.MessageEvent{
		{EventID: "e", Room: "", UserID: 1, Content: "x"},
		{EventID: "e", Room: "bad room", UserID: 1, Content: "x"},
		{EventID: "e", Room: "general", UserID: 0, Content: "x"},
		{EventID: "", Room: "general", UserID: 1, Content: "x"},
		{EventID: "e", Room: "general", UserID: 1, Content: ""},
	}
	for i, event := range bad {
		if err := f.ValidateEvent(event); !errors.Is(err, ErrValidation) {
			t.Errorf("case %d: ValidateEvent = %v, want ErrValidation", i, err)
		}
	}

	if err := f.ValidateEvent(msg); err != nil {
		t.Errorf("valid event rejected: %v", err)
	}
}
