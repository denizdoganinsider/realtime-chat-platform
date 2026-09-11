package repository

import (
	"database/sql"
	"errors"

	"realtime-chat-platform/notification-service/internal/domain"

	"github.com/go-sql-driver/mysql"
)

// ErrDuplicate is the unique (event_id, user_id) key firing: this recipient
// already has a row for this event. Distinguished from every other error so a
// replayed event and a database outage are not the same log line.
var ErrDuplicate = errors.New("delivery already recorded")

const mysqlDuplicateEntry = 1062

type DeliveryRepository struct {
	db *sql.DB
}

func NewDeliveryRepository(db *sql.DB) *DeliveryRepository {
	return &DeliveryRepository{db: db}
}

func (r *DeliveryRepository) Create(delivery *domain.Delivery) error {
	result, err := r.db.Exec(
		`INSERT INTO deliveries (event_id, user_id, room, url, status) VALUES (?, ?, ?, ?, ?)`,
		delivery.EventID, delivery.UserID, delivery.Room, delivery.URL, domain.DeliveryStatusPending,
	)
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == mysqlDuplicateEntry {
		return ErrDuplicate
	}
	if err != nil {
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	delivery.ID = id

	return nil
}

func (r *DeliveryRepository) RecordAttempt(id int64, status domain.DeliveryStatus, attempts int, lastStatus *int, lastError *string) error {
	_, err := r.db.Exec(
		`UPDATE deliveries SET status = ?, attempts = ?, last_status = ?, last_error = ? WHERE id = ?`,
		status, attempts, lastStatus, lastError, id,
	)
	return err
}

func (r *DeliveryRepository) ListByUserID(userID int64, limit int) ([]domain.Delivery, error) {
	rows, err := r.db.Query(
		`SELECT id, event_id, user_id, room, url, status, attempts, last_status, last_error, created_at, updated_at
		 FROM deliveries WHERE user_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`,
		userID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	deliveries := make([]domain.Delivery, 0)
	for rows.Next() {
		var d domain.Delivery
		var lastStatus sql.NullInt64
		var lastError sql.NullString
		if err := rows.Scan(&d.ID, &d.EventID, &d.UserID, &d.Room, &d.URL, &d.Status, &d.Attempts, &lastStatus, &lastError, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		if lastStatus.Valid {
			v := int(lastStatus.Int64)
			d.LastStatus = &v
		}
		if lastError.Valid {
			v := lastError.String
			d.LastError = &v
		}
		deliveries = append(deliveries, d)
	}

	return deliveries, rows.Err()
}
