package service

import (
	"realtime-chat-platform/notification-service/internal/domain"
	"realtime-chat-platform/notification-service/internal/repository"
)

type SubscriptionService struct {
	subscriptionRepo repository.SubscriptionRepositoryInterface
}

func NewSubscriptionService(subscriptionRepo repository.SubscriptionRepositoryInterface) *SubscriptionService {
	return &SubscriptionService{subscriptionRepo: subscriptionRepo}
}

func (s *SubscriptionService) Subscribe(userID int64, room string) error {
	if err := validateRoom(room); err != nil {
		return err
	}
	return s.subscriptionRepo.Create(userID, room)
}

func (s *SubscriptionService) Unsubscribe(userID int64, room string) error {
	if err := validateRoom(room); err != nil {
		return err
	}
	return s.subscriptionRepo.Delete(userID, room)
}

func (s *SubscriptionService) List(userID int64) ([]domain.Subscription, error) {
	return s.subscriptionRepo.ListByUserID(userID)
}
