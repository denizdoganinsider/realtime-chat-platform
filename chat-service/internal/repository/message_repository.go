package repository

import (
	"database/sql"

	"realtime-chat-platform/chat-service/internal/domain"
)

type MessageRepository struct {
	db *sql.DB
}

func NewMessageRepository(db *sql.DB) *MessageRepository {
	return &MessageRepository{db: db}
}

// Create writes created_at explicitly rather than letting the column default
// fill it in: the timestamp is the one the room stamped on the broadcast, so
// the stored row and what every connected client saw agree exactly.
func (r *MessageRepository) Create(message *domain.StoredMessage) error {
	query := `
		INSERT INTO messages (room_id, user_id, content, created_at)
		VALUES (?, ?, ?, ?)
	`

	result, err := r.db.Exec(query, message.RoomID, message.UserID, message.Content, message.CreatedAt)
	if err != nil {
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}

	message.ID = id

	return nil
}

// The ", id DESC" tiebreak is not cosmetic: without it, messages sharing a
// created_at can shuffle between pages, so LIMIT/OFFSET can show a row twice or
// skip it entirely. It is also free - InnoDB appends the primary key to every
// secondary index, so idx_messages_room_created already sorts by it.
func (r *MessageRepository) ListByRoom(roomID string, limit int, offset int) ([]domain.StoredMessage, error) {
	query := `
		SELECT id, room_id, user_id, content, created_at
		FROM messages
		WHERE room_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ? OFFSET ?
	`

	rows, err := r.db.Query(query, roomID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Non-nil empty slice so an empty room serialises as [] instead of null.
	messages := make([]domain.StoredMessage, 0, limit)
	for rows.Next() {
		var message domain.StoredMessage
		if err := rows.Scan(
			&message.ID,
			&message.RoomID,
			&message.UserID,
			&message.Content,
			&message.CreatedAt,
		); err != nil {
			return nil, err
		}

		messages = append(messages, message)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return messages, nil
}

func (r *MessageRepository) CountByRoom(roomID string) (int64, error) {
	query := `
		SELECT COUNT(*)
		FROM messages
		WHERE room_id = ?
	`

	var total int64
	if err := r.db.QueryRow(query, roomID).Scan(&total); err != nil {
		return 0, err
	}

	return total, nil
}
