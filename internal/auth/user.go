package auth

import "time"

// User представляет пользователя приложения
type User struct {
	ID           uint64
	Email        string
	PasswordHash string
	Name         string
	CreatedAt    time.Time
}

// NewUser содержит данные для создания пользователя
type NewUser struct {
	Email        string
	PasswordHash string
	Name         string
}
