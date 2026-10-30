package tasks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"task-tracker/internal/teams"
)

var (
	ErrInvalidTeamID     = errors.New("tasks: invalid team id")
	ErrInvalidTaskID     = errors.New("tasks: invalid task id")
	ErrInvalidTaskTitle  = errors.New("tasks: invalid task title")
	ErrInvalidTaskStatus = errors.New("tasks: invalid task status")
	ErrInvalidAssignee   = errors.New("tasks: invalid assignee")
	ErrInvalidPagination = errors.New("tasks: invalid pagination")
	ErrInvalidVersion    = errors.New("tasks: invalid version")
	ErrAccessDenied      = errors.New("tasks: access denied")
	ErrAssigneeNotMember = errors.New("tasks: assignee is not a team member")
)

type taskRepository interface {
	Create(ctx context.Context, creatorID uint64, input CreateInput, closedAt *time.Time) (Task, error)
	GetByID(ctx context.Context, taskID uint64) (Task, error)
	List(ctx context.Context, filter ListFilter) ([]Task, error)
	Update(ctx context.Context, taskID, changedBy, expectedVersion uint64, input UpdateInput) (Task, error)
	HistoryByTask(ctx context.Context, taskID uint64) ([]HistoryEntry, error)
}

type teamAccess interface {
	MembershipRole(ctx context.Context, teamID, userID uint64) (teams.Role, error)
}

// Service реализует сценарии работы с задачами
type Service struct {
	repository taskRepository
	teams      teamAccess
	cache      taskListCache
}

// NewService создаёт сервис задач
func NewService(repository taskRepository, teams teamAccess, cache taskListCache) *Service {
	return &Service{
		repository: repository,
		teams:      teams,
		cache:      cache,
	}
}

// TaskTeamID возвращает идентификатор команды существующей задачи
func (s *Service) TaskTeamID(ctx context.Context, taskID uint64) (uint64, error) {
	if taskID == 0 {
		return 0, ErrInvalidTaskID
	}
	task, err := s.repository.GetByID(ctx, taskID)
	if err != nil {
		return 0, fmt.Errorf("tasks service: get task team id: %w", err)
	}
	return task.TeamID, nil
}

// Create создаёт задачу от имени текущего пользователя
func (s *Service) Create(ctx context.Context, userID uint64, input CreateInput) (Task, error) {
	if input.TeamID == 0 {
		return Task{}, ErrInvalidTeamID
	}
	title, err := normalizeTitle(input.Title)
	if err != nil {
		return Task{}, err
	}
	input.Title = title

	if input.Status == "" {
		input.Status = StatusTodo
	}
	if !validStatus(input.Status) {
		return Task{}, ErrInvalidTaskStatus
	}
	if input.AssigneeID != nil && *input.AssigneeID == 0 {
		return Task{}, ErrInvalidAssignee
	}

	if _, err := s.membershipRole(ctx, input.TeamID, userID); err != nil {
		return Task{}, err
	}
	if input.AssigneeID != nil {
		if _, err := s.teams.MembershipRole(ctx, input.TeamID, *input.AssigneeID); err != nil {
			if errors.Is(err, teams.ErrMembershipNotFound) {
				return Task{}, ErrAssigneeNotMember
			}
			return Task{}, fmt.Errorf("tasks service: check assignee membership: %w", err)
		}
	}

	var closedAt *time.Time
	if terminalStatus(input.Status) {
		now := time.Now().UTC()
		closedAt = &now
	}

	task, err := s.repository.Create(ctx, userID, input, closedAt)
	if err != nil {
		return Task{}, fmt.Errorf("tasks service: create task: %w", err)
	}
	s.invalidate(ctx, task.TeamID)
	return task, nil
}

// List возвращает задачи доступной пользователю команды
func (s *Service) List(ctx context.Context, userID uint64, filter ListFilter) ([]Task, error) {
	if filter.TeamID == 0 {
		return nil, ErrInvalidTeamID
	}
	if filter.Status != nil && !validStatus(*filter.Status) {
		return nil, ErrInvalidTaskStatus
	}
	if filter.AssigneeID != nil && *filter.AssigneeID == 0 {
		return nil, ErrInvalidAssignee
	}
	if filter.Limit < 1 || filter.Limit > 100 || filter.Offset < 0 {
		return nil, ErrInvalidPagination
	}

	if _, err := s.membershipRole(ctx, filter.TeamID, userID); err != nil {
		return nil, err
	}

	cachedTasks, found, cacheKey, err := s.cache.Get(ctx, filter)
	if err != nil {
		slog.Warn("read task list cache", "error", err, "team_id", filter.TeamID)
		cacheKey = ""
	} else if found {
		return cachedTasks, nil
	}

	result, err := s.repository.List(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("tasks service: list tasks: %w", err)
	}
	if err := s.cache.Set(ctx, cacheKey, result); err != nil {
		slog.Warn("write task list cache", "error", err, "team_id", filter.TeamID)
	}
	return result, nil
}

// Update изменяет задачу с проверкой прав и версии
func (s *Service) Update(
	ctx context.Context,
	userID, taskID uint64,
	input UpdateInput,
) (Task, error) {
	if taskID == 0 {
		return Task{}, ErrInvalidTaskID
	}
	if input.Version == 0 {
		return Task{}, ErrInvalidVersion
	}

	current, err := s.repository.GetByID(ctx, taskID)
	if err != nil {
		return Task{}, fmt.Errorf("tasks service: get task for update: %w", err)
	}

	role, err := s.membershipRole(ctx, current.TeamID, userID)
	if err != nil {
		if errors.Is(err, ErrAccessDenied) {
			return Task{}, ErrTaskNotFound
		}
		return Task{}, err
	}
	if current.Version != input.Version {
		return Task{}, ErrVersionConflict
	}
	if err := authorizeUpdate(userID, role, current, input); err != nil {
		return Task{}, err
	}
	if err := normalizeUpdate(&input); err != nil {
		return Task{}, err
	}
	if input.AssigneeIDSet && input.AssigneeID != nil {
		if _, err := s.teams.MembershipRole(ctx, current.TeamID, *input.AssigneeID); err != nil {
			if errors.Is(err, teams.ErrMembershipNotFound) {
				return Task{}, ErrAssigneeNotMember
			}
			return Task{}, fmt.Errorf("tasks service: check updated assignee membership: %w", err)
		}
	}

	updated, err := s.repository.Update(ctx, taskID, userID, input.Version, input)
	if err != nil {
		return Task{}, fmt.Errorf("tasks service: update task: %w", err)
	}
	if updated.Version != current.Version {
		s.invalidate(ctx, current.TeamID)
	}
	return updated, nil
}

// History возвращает историю доступной пользователю задачи
func (s *Service) History(ctx context.Context, userID, taskID uint64) ([]HistoryEntry, error) {
	if taskID == 0 {
		return nil, ErrInvalidTaskID
	}
	task, err := s.repository.GetByID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("tasks service: get task for history: %w", err)
	}
	if _, err := s.membershipRole(ctx, task.TeamID, userID); err != nil {
		if errors.Is(err, ErrAccessDenied) {
			return nil, ErrTaskNotFound
		}
		return nil, err
	}

	history, err := s.repository.HistoryByTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("tasks service: list history: %w", err)
	}
	return history, nil
}

func (s *Service) membershipRole(ctx context.Context, teamID, userID uint64) (teams.Role, error) {
	role, err := s.teams.MembershipRole(ctx, teamID, userID)
	if err != nil {
		if errors.Is(err, teams.ErrMembershipNotFound) {
			return "", ErrAccessDenied
		}
		return "", fmt.Errorf("tasks service: get membership role: %w", err)
	}
	return role, nil
}

func (s *Service) invalidate(ctx context.Context, teamID uint64) {
	if err := s.cache.Invalidate(ctx, teamID); err != nil {
		slog.Warn("invalidate task list cache", "error", err, "team_id", teamID)
	}
}

func authorizeUpdate(userID uint64, role teams.Role, task Task, input UpdateInput) error {
	if role == teams.RoleOwner || role == teams.RoleAdmin || userID == task.CreatedBy {
		return nil
	}
	if task.AssigneeID == nil || *task.AssigneeID != userID {
		return ErrAccessDenied
	}
	if input.TitleSet || input.DescriptionSet || input.AssigneeIDSet {
		return ErrAccessDenied
	}
	return nil
}

func normalizeUpdate(input *UpdateInput) error {
	if input.TitleSet {
		title, err := normalizeTitle(input.Title)
		if err != nil {
			return err
		}
		input.Title = title
	}
	if input.StatusSet && !validStatus(input.Status) {
		return ErrInvalidTaskStatus
	}
	if input.AssigneeIDSet && input.AssigneeID != nil && *input.AssigneeID == 0 {
		return ErrInvalidAssignee
	}
	return nil
}

func normalizeTitle(value string) (string, error) {
	title := strings.TrimSpace(value)
	if title == "" || utf8.RuneCountInString(title) > 255 {
		return "", ErrInvalidTaskTitle
	}
	return title, nil
}
