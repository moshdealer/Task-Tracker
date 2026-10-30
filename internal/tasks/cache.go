package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"
)

const taskListCacheTTL = 5 * time.Minute

type cacheStore interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	Increment(ctx context.Context, key string) (uint64, error)
}

type taskListCache interface {
	Get(ctx context.Context, filter ListFilter) ([]Task, bool, string, error)
	Set(ctx context.Context, key string, tasks []Task) error
	Invalidate(ctx context.Context, teamID uint64) error
}

// ListCache кеширует списки задач и версии кеша команд
type ListCache struct {
	store cacheStore
	dirty sync.Map
}

// NewListCache создаёт кеш списков задач
func NewListCache(store cacheStore) *ListCache {
	return &ListCache{store: store}
}

// Get возвращает кешированный список и ключ для безопасного последующего заполнения
func (c *ListCache) Get(
	ctx context.Context,
	filter ListFilter,
) ([]Task, bool, string, error) {
	if _, dirty := c.dirty.Load(filter.TeamID); dirty {
		return nil, false, "", nil
	}

	generation, err := c.generation(ctx, filter.TeamID)
	if err != nil {
		return nil, false, "", err
	}
	key := listCacheKey(filter, generation)
	encoded, found, err := c.store.Get(ctx, key)
	if err != nil || !found {
		return nil, false, key, err
	}

	var tasks []Task
	if err := json.Unmarshal([]byte(encoded), &tasks); err != nil {
		return nil, false, key, fmt.Errorf("tasks cache: decode list: %w", err)
	}
	if tasks == nil {
		tasks = make([]Task, 0)
	}
	return tasks, true, key, nil
}

// Set сохраняет список по ключу, полученному до чтения MySQL
func (c *ListCache) Set(ctx context.Context, key string, tasks []Task) error {
	if key == "" {
		return nil
	}
	encoded, err := json.Marshal(tasks)
	if err != nil {
		return fmt.Errorf("tasks cache: encode list: %w", err)
	}
	if err := c.store.Set(ctx, key, string(encoded), taskListCacheTTL); err != nil {
		return fmt.Errorf("tasks cache: store list: %w", err)
	}
	return nil
}

// Invalidate переключает поколение кеша команды
func (c *ListCache) Invalidate(ctx context.Context, teamID uint64) error {
	if _, err := c.store.Increment(ctx, generationCacheKey(teamID)); err != nil {
		c.dirty.Store(teamID, struct{}{})
		return fmt.Errorf("tasks cache: invalidate team %d: %w", teamID, err)
	}
	c.dirty.Delete(teamID)
	return nil
}

func (c *ListCache) generation(ctx context.Context, teamID uint64) (uint64, error) {
	encoded, found, err := c.store.Get(ctx, generationCacheKey(teamID))
	if err != nil {
		return 0, fmt.Errorf("tasks cache: get generation: %w", err)
	}
	if !found {
		return 0, nil
	}
	generation, err := strconv.ParseUint(encoded, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("tasks cache: parse generation: %w", err)
	}
	return generation, nil
}

func generationCacheKey(teamID uint64) string {
	return fmt.Sprintf("tasks:list:team:%d:generation", teamID)
}

func listCacheKey(filter ListFilter, generation uint64) string {
	status := "all"
	if filter.Status != nil {
		status = string(*filter.Status)
	}
	assignee := "all"
	if filter.AssigneeID != nil {
		assignee = strconv.FormatUint(*filter.AssigneeID, 10)
	}
	return fmt.Sprintf(
		"tasks:list:team:%d:g:%d:status:%s:assignee:%s:limit:%d:offset:%d",
		filter.TeamID,
		generation,
		status,
		assignee,
		filter.Limit,
		filter.Offset,
	)
}
