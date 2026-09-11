package repository

import (
	"database/sql"

	"realtime-chat-platform/notification-service/internal/domain"
)

type SubscriptionRepository struct {
	db *sql.DB
}

func NewSubscriptionRepository(db *sql.DB) *SubscriptionRepository {
	return &SubscriptionRepository{db: db}
}

// Create is idempotent: subscribing twice is one subscription, not an error.
func (r *SubscriptionRepository) Create(userID int64, room string) error {
	_, err := r.db.Exec(`INSERT IGNORE INTO subscriptions (user_id, room) VALUES (?, ?)`, userID, room)
	return err
}

func (r *SubscriptionRepository) Delete(userID int64, room string) error {
	_, err := r.db.Exec(`DELETE FROM subscriptions WHERE user_id = ? AND room = ?`, userID, room)
	return err
}

func (r *SubscriptionRepository) ListByUserID(userID int64) ([]domain.Subscription, error) {
	rows, err := r.db.Query(`SELECT user_id, room, created_at FROM subscriptions WHERE user_id = ? ORDER BY room`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Non-nil empty slice: no subscriptions must marshal to [] rather than null.
	subscriptions := make([]domain.Subscription, 0)
	for rows.Next() {
		var s domain.Subscription
		if err := rows.Scan(&s.UserID, &s.Room, &s.CreatedAt); err != nil {
			return nil, err
		}
		subscriptions = append(subscriptions, s)
	}

	return subscriptions, rows.Err()
}

func (r *SubscriptionRepository) ListUserIDsByRoom(room string) ([]int64, error) {
	rows, err := r.db.Query(`SELECT user_id FROM subscriptions WHERE room = ?`, room)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var userIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		userIDs = append(userIDs, id)
	}

	return userIDs, rows.Err()
}
