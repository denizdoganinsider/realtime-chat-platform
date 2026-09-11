package service

import (
	"errors"
	"testing"

	"realtime-chat-platform/presence-service/internal/domain"
	"realtime-chat-platform/presence-service/internal/repository"
)

// recordingRepo captures what the service would write, so the validation layer
// can be exercised without Redis.
type recordingRepo struct {
	calls []string
}

func (r *recordingRepo) SetOnline(room string, userID int64, instanceID string) error {
	r.calls = append(r.calls, "online:"+room+":"+instanceID)
	return nil
}

func (r *recordingRepo) SetOffline(room string, userID int64, instanceID string) error {
	r.calls = append(r.calls, "offline:"+room+":"+instanceID)
	return nil
}

func (r *recordingRepo) Refresh(room string, instanceID string, userIDs []int64) error {
	r.calls = append(r.calls, "refresh:"+room+":"+instanceID)
	return nil
}

func (r *recordingRepo) ListOnline(room string) ([]int64, error) { return nil, nil }

func (r *recordingRepo) ListRooms() ([]repository.RoomCount, error) {
	return []repository.RoomCount{{Name: "general", Count: 2}}, nil
}

func TestRecordEventPassesInstanceThrough(t *testing.T) {
	repo := &recordingRepo{}
	s := NewPresenceService(repo)

	err := s.RecordEvent(domain.PresenceEvent{
		UserID: 1, Room: "general", Status: domain.PresenceStatusOnline, InstanceID: "chat-8001",
	})
	if err != nil {
		t.Fatalf("RecordEvent returned error: %v", err)
	}

	if len(repo.calls) != 1 || repo.calls[0] != "online:general:chat-8001" {
		t.Errorf("repo calls = %v, want [online:general:chat-8001]", repo.calls)
	}
}

// Every rejection here is a 400 to chat-service, not a 500: none of these
// would become valid by being retried.
func TestInstanceIDValidation(t *testing.T) {
	cases := map[string]string{
		"missing":        "",
		"colon":          "chat:8001", // would make the Redis member ambiguous
		"space":          "chat 8001",
		"slash":          "chat/8001",
		"too long":       "chat-" + string(make([]byte, 70)),
		"unicode letter": "chät",
	}

	for name, instanceID := range cases {
		t.Run(name, func(t *testing.T) {
			repo := &recordingRepo{}
			s := NewPresenceService(repo)

			err := s.RecordEvent(domain.PresenceEvent{
				UserID: 1, Room: "general", Status: domain.PresenceStatusOnline, InstanceID: instanceID,
			})
			if !errors.Is(err, ErrValidation) {
				t.Errorf("RecordEvent(%q) error = %v, want ErrValidation", instanceID, err)
			}

			err = s.Heartbeat("general", instanceID, []int64{1})
			if !errors.Is(err, ErrValidation) {
				t.Errorf("Heartbeat(%q) error = %v, want ErrValidation", instanceID, err)
			}

			if len(repo.calls) != 0 {
				t.Errorf("repo was called despite validation failure: %v", repo.calls)
			}
		})
	}
}

func TestInstanceIDAllowsHostnames(t *testing.T) {
	s := NewPresenceService(&recordingRepo{})

	for _, instanceID := range []string{"chat-8001", "chat-svc-0.chat.svc.cluster.local", "A_b-1"} {
		if err := s.Heartbeat("general", instanceID, []int64{1}); err != nil {
			t.Errorf("Heartbeat(%q) returned error: %v", instanceID, err)
		}
	}
}

func TestListRoomsMapsToDomain(t *testing.T) {
	s := NewPresenceService(&recordingRepo{})

	rooms, err := s.ListRooms()
	if err != nil {
		t.Fatalf("ListRooms returned error: %v", err)
	}

	if len(rooms) != 1 || rooms[0].Name != "general" || rooms[0].Count != 2 {
		t.Errorf("ListRooms = %+v, want [{general 2}]", rooms)
	}
}
