package jwt

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
	"task-tracker/internal/platform/config"
)

var ErrInvalidToken = errors.New("jwt: invalid token")

// Manager выпускает и проверяет JWT
type Manager struct {
	secret []byte
	ttl    time.Duration
}

// New создаёт менеджер JWT
func New(cfg config.JWT) *Manager {
	return &Manager{
		secret: []byte(cfg.Secret),
		ttl:    cfg.TTL,
	}
}

// Issue выпускает JWT для пользователя
func (m *Manager) Issue(userID uint64) (string, error) {
	now := time.Now()
	claims := jwtlib.RegisteredClaims{
		Subject:   strconv.FormatUint(userID, 10),
		IssuedAt:  jwtlib.NewNumericDate(now),
		ExpiresAt: jwtlib.NewNumericDate(now.Add(m.ttl)),
	}
	token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims)

	signedToken, err := token.SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("jwt: sign token: %w", err)
	}

	return signedToken, nil
}

// Parse проверяет JWT и возвращает идентификатор пользователя
func (m *Manager) Parse(tokenValue string) (uint64, error) {
	claims := &jwtlib.RegisteredClaims{}
	parser := jwtlib.NewParser(
		jwtlib.WithValidMethods([]string{jwtlib.SigningMethodHS256.Alg()}),
		jwtlib.WithExpirationRequired(),
	)

	token, err := parser.ParseWithClaims(
		tokenValue,
		claims,
		func(*jwtlib.Token) (any, error) {
			return m.secret, nil
		},
	)
	if err != nil || !token.Valid {
		return 0, ErrInvalidToken
	}

	userID, err := strconv.ParseUint(claims.Subject, 10, 64)
	if err != nil || userID == 0 {
		return 0, ErrInvalidToken
	}

	return userID, nil
}
