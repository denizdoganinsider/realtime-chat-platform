package repository

import (
	"database/sql"
	"errors"
	"strings"

	"realtime-chat-platform/notification-service/internal/domain"
)

var ErrNotFound = errors.New("not found")

type WebhookRepository struct {
	db *sql.DB
}

func NewWebhookRepository(db *sql.DB) *WebhookRepository {
	return &WebhookRepository{db: db}
}

// Upsert: one endpoint per user, PUT semantics. A re-PUT rotates the secret too,
// which is the only way a user ever gets a new one.
func (r *WebhookRepository) Upsert(webhook *domain.Webhook) error {
	query := `
	INSERT INTO webhooks (user_id, url, secret)
	VALUES (?, ?, ?)
	ON DUPLICATE KEY UPDATE url = VALUES(url), secret = VALUES(secret)
	`

	_, err := r.db.Exec(query, webhook.UserID, webhook.URL, webhook.Secret)

	return err
}

func (r *WebhookRepository) GetByUserID(userID int64) (*domain.Webhook, error) {
	query := `SELECT user_id, url, secret, created_at, updated_at FROM webhooks WHERE user_id = ?`

	var w domain.Webhook
	err := r.db.QueryRow(query, userID).Scan(&w.UserID, &w.URL, &w.Secret, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	return &w, nil
}

func (r *WebhookRepository) DeleteByUserID(userID int64) error {
	_, err := r.db.Exec(`DELETE FROM webhooks WHERE user_id = ?`, userID)
	return err
}

// ListByUserIDs is the fan-out read: one query for every recipient of an event
// rather than one per recipient.
func (r *WebhookRepository) ListByUserIDs(userIDs []int64) ([]domain.Webhook, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(userIDs)), ",")
	args := make([]any, 0, len(userIDs))
	for _, id := range userIDs {
		args = append(args, id)
	}

	rows, err := r.db.Query(`SELECT user_id, url, secret, created_at, updated_at FROM webhooks WHERE user_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var webhooks []domain.Webhook
	for rows.Next() {
		var w domain.Webhook
		if err := rows.Scan(&w.UserID, &w.URL, &w.Secret, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, err
		}
		webhooks = append(webhooks, w)
	}

	return webhooks, rows.Err()
}
