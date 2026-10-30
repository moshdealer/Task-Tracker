package cache

import (
	"context"
	"fmt"
	"time"

	redislib "github.com/redis/go-redis/v9"
	"task-tracker/internal/platform/config"
)

// Redis - минимальные операции кеша приложения
type Redis struct {
	client *redislib.Client
}

// NewRedis создаёт клиент Redis и проверяет соединение
func NewRedis(ctx context.Context, cfg config.Redis) (*Redis, error) {
	options, err := redislib.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("cache: parse Redis URL: %w", err)
	}

	client := redislib.NewClient(options)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("cache: ping Redis: %w", err)
	}

	return &Redis{client: client}, nil
}

// Get возвращает значение и признак существования ключа
func (r *Redis) Get(ctx context.Context, key string) (string, bool, error) {
	value, err := r.client.Get(ctx, key).Result()
	if err == nil {
		return value, true, nil
	}
	if err == redislib.Nil {
		return "", false, nil
	}
	return "", false, fmt.Errorf("cache: get %q: %w", key, err)
}

// Set сохраняет значение с заданным временем жизни
func (r *Redis) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if err := r.client.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("cache: set %q: %w", key, err)
	}
	return nil
}

// Increment атомарно увеличивает числовое значение ключа
func (r *Redis) Increment(ctx context.Context, key string) (uint64, error) {
	value, err := r.client.Incr(ctx, key).Uint64()
	if err != nil {
		return 0, fmt.Errorf("cache: increment %q: %w", key, err)
	}
	return value, nil
}

// Close закрывает соединения клиента Redis
func (r *Redis) Close() error {
	if err := r.client.Close(); err != nil {
		return fmt.Errorf("cache: close Redis client: %w", err)
	}
	return nil
}
