package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	mysqldriver "github.com/go-sql-driver/mysql"
)

var (
	ErrUserNotFound       = errors.New("auth: user not found")
	ErrEmailAlreadyExists = errors.New("auth: email already exists")
)

// Repository хранит пользователей в MySQL
type Repository struct {
	db *sql.DB
}

// NewRepository создаёт репозиторий пользователей
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// Create создаёт пользователя и возвращает сохранённые данные
func (r *Repository) Create(ctx context.Context, newUser NewUser) (User, error) {
	result, err := r.db.ExecContext(
		ctx,
		`INSERT INTO users (email, password_hash, name) VALUES (?, ?, ?)`,
		newUser.Email,
		newUser.PasswordHash,
		newUser.Name,
	)
	if err != nil {
		var mysqlErr *mysqldriver.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return User{}, ErrEmailAlreadyExists
		}

		return User{}, fmt.Errorf("auth repository: create user: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("auth repository: get created user id: %w", err)
	}

	user, err := r.GetByID(ctx, uint64(id))
	if err != nil {
		return User{}, fmt.Errorf("auth repository: get created user: %w", err)
	}

	return user, nil
}

// GetByEmail возвращает пользователя по email
func (r *Repository) GetByEmail(ctx context.Context, email string) (User, error) {
	row := r.db.QueryRowContext(
		ctx,
		`SELECT id, email, password_hash, name, created_at FROM users WHERE email = ?`,
		email,
	)

	user, err := scanUser(row)
	if err != nil {
		return User{}, fmt.Errorf("auth repository: get user by email: %w", err)
	}

	return user, nil
}

// GetByID возвращает пользователя по идентификатору
func (r *Repository) GetByID(ctx context.Context, id uint64) (User, error) {
	row := r.db.QueryRowContext(
		ctx,
		`SELECT id, email, password_hash, name, created_at FROM users WHERE id = ?`,
		id,
	)

	user, err := scanUser(row)
	if err != nil {
		return User{}, fmt.Errorf("auth repository: get user by id: %w", err)
	}

	return user, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(row rowScanner) (User, error) {
	var user User
	if err := row.Scan(
		&user.ID,
		&user.Email,
		&user.PasswordHash,
		&user.Name,
		&user.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrUserNotFound
		}

		return User{}, fmt.Errorf("scan user: %w", err)
	}

	return user, nil
}
