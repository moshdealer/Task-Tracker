package auth

import "context"

type contextKey uint8

const userIDContextKey contextKey = iota

func contextWithUserID(ctx context.Context, userID uint64) context.Context {
	return context.WithValue(ctx, userIDContextKey, userID)
}

// UserIDFromContext возвращает идентификатор аутентифицированного пользователя
func UserIDFromContext(ctx context.Context) (uint64, bool) {
	userID, ok := ctx.Value(userIDContextKey).(uint64)
	return userID, ok && userID != 0
}
