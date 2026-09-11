package service

import (
	"crypto/rand"
	"encoding/hex"

	"realtime-chat-platform/notification-service/internal/domain"
	"realtime-chat-platform/notification-service/internal/repository"
)

type WebhookService struct {
	webhookRepo   repository.WebhookRepositoryInterface
	allowLoopback bool
}

func NewWebhookService(webhookRepo repository.WebhookRepositoryInterface, allowLoopback bool) *WebhookService {
	return &WebhookService{webhookRepo: webhookRepo, allowLoopback: allowLoopback}
}

// Put registers or replaces the caller's endpoint and mints a fresh secret. The
// secret is returned here and nowhere else.
func (s *WebhookService) Put(userID int64, rawURL string) (*domain.WebhookWithSecret, error) {
	if err := validateWebhookURL(rawURL, s.allowLoopback); err != nil {
		return nil, err
	}

	secret, err := newSecret()
	if err != nil {
		return nil, err
	}

	webhook := &domain.Webhook{UserID: userID, URL: rawURL, Secret: secret}
	if err := s.webhookRepo.Upsert(webhook); err != nil {
		return nil, err
	}

	stored, err := s.webhookRepo.GetByUserID(userID)
	if err != nil {
		return nil, err
	}

	return &domain.WebhookWithSecret{Webhook: *stored, Secret: secret}, nil
}

func (s *WebhookService) Get(userID int64) (*domain.Webhook, error) {
	return s.webhookRepo.GetByUserID(userID)
}

func (s *WebhookService) Delete(userID int64) error {
	return s.webhookRepo.DeleteByUserID(userID)
}

// 32 random bytes, hex encoded: the same shape as the gateway's WebSocket
// tickets, and comfortably more entropy than HMAC-SHA256 can use.
func newSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
