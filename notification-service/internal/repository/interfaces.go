package repository

import "realtime-chat-platform/notification-service/internal/domain"

type WebhookRepositoryInterface interface {
	Upsert(webhook *domain.Webhook) error
	GetByUserID(userID int64) (*domain.Webhook, error)
	DeleteByUserID(userID int64) error
	ListByUserIDs(userIDs []int64) ([]domain.Webhook, error)
}

type SubscriptionRepositoryInterface interface {
	Create(userID int64, room string) error
	Delete(userID int64, room string) error
	ListByUserID(userID int64) ([]domain.Subscription, error)
	ListUserIDsByRoom(room string) ([]int64, error)
}

type DeliveryRepositoryInterface interface {
	Create(delivery *domain.Delivery) error
	RecordAttempt(id int64, status domain.DeliveryStatus, attempts int, lastStatus *int, lastError *string) error
	ListByUserID(userID int64, limit int) ([]domain.Delivery, error)
}
