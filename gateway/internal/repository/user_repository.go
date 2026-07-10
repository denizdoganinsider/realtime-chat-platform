package repository

import (
	"database/sql"

	"realtime-chat-platform/gateway/internal/domain"
)

type UserRepository struct {
	db *sql.DB
}

func NewUserRepository(db *sql.DB) *UserRepository {
	return &UserRepository{db: db}
}

func (r *UserRepository) Create(user *domain.User) error {
	query := `
	INSERT INTO users (email, password_hash, role)
	VALUES (?, ?, ?)
	`

	result, err := r.db.Exec(query, user.Email, user.PasswordHash, user.Role)
	if err != nil {
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}

	user.ID = id

	return nil
}

func (r *UserRepository) GetByEmail(email string) (*domain.User, error) {
	query := `
	SELECT id, email, password_hash, role, created_at
	FROM users
	WHERE email = ?
	`

	row := r.db.QueryRow(query, email)

	var user domain.User

	err := row.Scan(
		&user.ID,
		&user.Email,
		&user.PasswordHash,
		&user.Role,
		&user.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	return &user, nil
}

func (r *UserRepository) GetByID(id int64) (*domain.User, error) {
	query := `
	SELECT id, email, password_hash, role, created_at
	FROM users
	WHERE id = ?
	`

	row := r.db.QueryRow(query, id)

	var user domain.User

	err := row.Scan(
		&user.ID,
		&user.Email,
		&user.PasswordHash,
		&user.Role,
		&user.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	return &user, nil
}
