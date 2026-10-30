package auth

import (
	"net/http"
	"strings"
)

type tokenParser interface {
	Parse(tokenValue string) (uint64, error)
}

// Middleware проверяет JWT и добавляет текущего пользователя в контекст запроса
type Middleware struct {
	tokens tokenParser
}

// NewMiddleware создаёт middleware аутентификации
func NewMiddleware(tokens tokenParser) *Middleware {
	return &Middleware{tokens: tokens}
}

// Authenticate пропускает только запросы с корректным Bearer JWT
func (m *Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		tokenValue, ok := bearerToken(request)
		if !ok {
			writeUnauthorized(writer)
			return
		}

		userID, err := m.tokens.Parse(tokenValue)
		if err != nil || userID == 0 {
			writeUnauthorized(writer)
			return
		}

		ctx := contextWithUserID(request.Context(), userID)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func bearerToken(request *http.Request) (string, bool) {
	values := request.Header.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}

	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}

	return parts[1], true
}

func writeUnauthorized(writer http.ResponseWriter) {
	writer.Header().Set("WWW-Authenticate", "Bearer")
	writeError(writer, http.StatusUnauthorized, "unauthorized")
}
