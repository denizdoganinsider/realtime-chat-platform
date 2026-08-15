package service

import (
	"time"

	"realtime-chat-platform/chat-service/internal/domain"
)

const (
	defaultPerPage = 50
	maxPerPage     = 100
)

type MessageRepositoryInterface interface {
	Create(message *domain.StoredMessage) error
	ListByRoom(roomID string, limit int, offset int) ([]domain.StoredMessage, error)
	CountByRoom(roomID string) (int64, error)
}

type PaginatedMessages struct {
	Data    []domain.StoredMessage `json:"data"`
	Total   int64                  `json:"total"`
	Page    int                    `json:"page"`
	PerPage int                    `json:"per_page"`
}

type MessageService struct {
	messageRepo MessageRepositoryInterface
}

func NewMessageService(messageRepo MessageRepositoryInterface) *MessageService {
	return &MessageService{messageRepo: messageRepo}
}

// createdAt is passed in rather than taken here: the caller (the room's
// broadcast loop) already stamped the message it sent to every client, and the
// stored row must carry that same instant.
func (s *MessageService) Create(room string, userID int64, content string, createdAt time.Time) error {
	message := &domain.StoredMessage{
		RoomID:    room,
		UserID:    userID,
		Content:   content,
		CreatedAt: createdAt,
	}

	return s.messageRepo.Create(message)
}

func (s *MessageService) List(room string, page int, perPage int) (*PaginatedMessages, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = defaultPerPage
	}
	if perPage > maxPerPage {
		perPage = maxPerPage
	}

	offset := (page - 1) * perPage

	messages, err := s.messageRepo.ListByRoom(room, perPage, offset)
	if err != nil {
		return nil, err
	}

	total, err := s.messageRepo.CountByRoom(room)
	if err != nil {
		return nil, err
	}

	return &PaginatedMessages{
		Data:    messages,
		Total:   total,
		Page:    page,
		PerPage: perPage,
	}, nil
}
