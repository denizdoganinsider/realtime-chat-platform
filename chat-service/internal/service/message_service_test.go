package service

import (
	"testing"
	"time"

	"realtime-chat-platform/chat-service/internal/domain"
)

type stubMessageRepository struct {
	lastLimit  int
	lastOffset int
	total      int64
}

func (r *stubMessageRepository) Create(message *domain.StoredMessage) error {
	return nil
}

func (r *stubMessageRepository) ListByRoom(roomID string, limit int, offset int) ([]domain.StoredMessage, error) {
	r.lastLimit = limit
	r.lastOffset = offset
	return []domain.StoredMessage{}, nil
}

func (r *stubMessageRepository) CountByRoom(roomID string) (int64, error) {
	return r.total, nil
}

// Pagination inputs come straight off the query string, so the clamping is the
// only thing between a caller and "per_page=1000000".
func TestListClampsPagination(t *testing.T) {
	tests := []struct {
		name        string
		page        int
		perPage     int
		wantLimit   int
		wantOffset  int
		wantPage    int
		wantPerPage int
	}{
		{name: "defaults", page: 0, perPage: 0, wantLimit: 50, wantOffset: 0, wantPage: 1, wantPerPage: 50},
		{name: "negative page", page: -3, perPage: 10, wantLimit: 10, wantOffset: 0, wantPage: 1, wantPerPage: 10},
		{name: "second page", page: 2, perPage: 10, wantLimit: 10, wantOffset: 10, wantPage: 2, wantPerPage: 10},
		{name: "over the cap", page: 1, perPage: 5000, wantLimit: 100, wantOffset: 0, wantPage: 1, wantPerPage: 100},
		{name: "at the cap", page: 3, perPage: 100, wantLimit: 100, wantOffset: 200, wantPage: 3, wantPerPage: 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &stubMessageRepository{total: 7}
			s := NewMessageService(repo)

			result, err := s.List("general", tt.page, tt.perPage)
			if err != nil {
				t.Fatalf("List returned error: %v", err)
			}

			if repo.lastLimit != tt.wantLimit || repo.lastOffset != tt.wantOffset {
				t.Errorf("repository called with limit=%d offset=%d, want limit=%d offset=%d",
					repo.lastLimit, repo.lastOffset, tt.wantLimit, tt.wantOffset)
			}

			if result.Page != tt.wantPage || result.PerPage != tt.wantPerPage {
				t.Errorf("response page=%d per_page=%d, want page=%d per_page=%d",
					result.Page, result.PerPage, tt.wantPage, tt.wantPerPage)
			}

			if result.Total != 7 {
				t.Errorf("total = %d, want 7", result.Total)
			}
		})
	}
}

// An empty room must serialise as [] rather than null, which means the slice has
// to survive the service layer non-nil.
func TestListReturnsEmptySliceNotNil(t *testing.T) {
	s := NewMessageService(&stubMessageRepository{})

	result, err := s.List("general", 1, 10)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	if result.Data == nil {
		t.Error("Data is nil; an empty room would serialise as null instead of []")
	}
}

// The stored timestamp is the one the room stamped on the broadcast, not one
// invented at insert time - otherwise history and what clients saw disagree.
func TestCreatePassesThroughTheBroadcastTimestamp(t *testing.T) {
	var captured *domain.StoredMessage
	repo := &capturingRepository{onCreate: func(m *domain.StoredMessage) { captured = m }}
	s := NewMessageService(repo)

	broadcastAt := time.Date(2026, 8, 15, 10, 30, 0, 0, time.UTC)
	if err := s.Create("general", 42, "hello", broadcastAt); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if !captured.CreatedAt.Equal(broadcastAt) {
		t.Errorf("CreatedAt = %v, want %v", captured.CreatedAt, broadcastAt)
	}
	if captured.RoomID != "general" || captured.UserID != 42 || captured.Content != "hello" {
		t.Errorf("stored message = %+v, want room=general user=42 content=hello", captured)
	}
}

type capturingRepository struct {
	stubMessageRepository
	onCreate func(*domain.StoredMessage)
}

func (r *capturingRepository) Create(message *domain.StoredMessage) error {
	r.onCreate(message)
	return nil
}
