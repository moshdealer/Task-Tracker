package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidEmail       = errors.New("auth: invalid email")
	ErrInvalidPassword    = errors.New("auth: invalid password")
	ErrInvalidName        = errors.New("auth: invalid name")
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
)

type userRepository interface {
	Create(ctx context.Context, newUser NewUser) (User, error)
	GetByEmail(ctx context.Context, email string) (User, error)
}

type tokenIssuer interface {
	Issue(userID uint64) (string, error)
}

// Service реализует сценарии регистрации и аутентификации
type Service struct {
	users  userRepository
	tokens tokenIssuer
}

// NewService создаёт сервис аутентификации
func NewService(users userRepository, tokens tokenIssuer) *Service {
	return &Service{
		users:  users,
		tokens: tokens,
	}
}

// RegisterInput содержит данные регистрации
type RegisterInput struct {
	Email    string
	Password string
	Name     string
}

// LoginInput содержит данные аутентификации
type LoginInput struct {
	Email    string
	Password string
}

// Register регистрирует нового пользователя
func (s *Service) Register(ctx context.Context, input RegisterInput) (User, error) {
	email, err := normalizeEmail(input.Email)
	if err != nil {
		return User{}, err
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		return User{}, ErrInvalidName
	}
	if input.Password == "" {
		return User{}, ErrInvalidPassword
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		if errors.Is(err, bcrypt.ErrPasswordTooLong) {
			return User{}, ErrInvalidPassword
		}

		return User{}, fmt.Errorf("auth service: hash password: %w", err)
	}

	user, err := s.users.Create(ctx, NewUser{
		Email:        email,
		PasswordHash: string(passwordHash),
		Name:         name,
	})
	if err != nil {
		return User{}, fmt.Errorf("auth service: register user: %w", err)
	}

	return user, nil
}

// Login проверяет учётные данные и выдаёт JWT
func (s *Service) Login(ctx context.Context, input LoginInput) (string, error) {
	email, err := normalizeEmail(input.Email)
	if err != nil || input.Password == "" {
		return "", ErrInvalidCredentials
	}

	user, err := s.users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return "", ErrInvalidCredentials
		}

		return "", fmt.Errorf("auth service: get user: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(input.Password)); err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return "", ErrInvalidCredentials
		}

		return "", fmt.Errorf("auth service: compare password: %w", err)
	}

	token, err := s.tokens.Issue(user.ID)
	if err != nil {
		return "", fmt.Errorf("auth service: issue token: %w", err)
	}

	return token, nil
}

func normalizeEmail(value string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return "", ErrInvalidEmail
	}

	return email, nil
}
