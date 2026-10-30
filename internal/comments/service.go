package comments

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"task-tracker/internal/tasks"
	"task-tracker/internal/teams"
)

var (
	ErrInvalidTaskID  = errors.New("comments: invalid task id")
	ErrInvalidContent = errors.New("comments: invalid content")
	ErrTaskNotFound   = errors.New("comments: task not found")
)

type commentRepository interface {
	Create(ctx context.Context, taskID, userID uint64, content string) (Comment, error)
	ListByTask(ctx context.Context, taskID uint64) ([]Comment, error)
}

type taskAccess interface {
	TaskTeamID(ctx context.Context, taskID uint64) (uint64, error)
}

type teamAccess interface {
	MembershipRole(ctx context.Context, teamID, userID uint64) (teams.Role, error)
}

// Service реализует сценарии работы с комментариями
type Service struct {
	repository commentRepository
	tasks      taskAccess
	teams      teamAccess
}

// NewService создаёт сервис комментариев
func NewService(repository commentRepository, tasks taskAccess, teams teamAccess) *Service {
	return &Service{
		repository: repository,
		tasks:      tasks,
		teams:      teams,
	}
}

// Create добавляет комментарий текущего пользователя к доступной задаче
func (s *Service) Create(
	ctx context.Context,
	userID, taskID uint64,
	content string,
) (Comment, error) {
	if taskID == 0 {
		return Comment{}, ErrInvalidTaskID
	}

	content = strings.TrimSpace(content)
	if content == "" {
		return Comment{}, ErrInvalidContent
	}

	if err := s.authorize(ctx, userID, taskID); err != nil {
		return Comment{}, err
	}

	comment, err := s.repository.Create(ctx, taskID, userID, content)
	if err != nil {
		return Comment{}, fmt.Errorf("comments service: create comment: %w", err)
	}

	return comment, nil
}

// List возвращает комментарии доступной пользователю задачи
func (s *Service) List(ctx context.Context, userID, taskID uint64) ([]Comment, error) {
	if taskID == 0 {
		return nil, ErrInvalidTaskID
	}

	if err := s.authorize(ctx, userID, taskID); err != nil {
		return nil, err
	}

	result, err := s.repository.ListByTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("comments service: list comments: %w", err)
	}

	return result, nil
}

func (s *Service) authorize(ctx context.Context, userID, taskID uint64) error {
	teamID, err := s.tasks.TaskTeamID(ctx, taskID)
	if err != nil {
		if errors.Is(err, tasks.ErrTaskNotFound) {
			return ErrTaskNotFound
		}
		return fmt.Errorf("comments service: get task team: %w", err)
	}

	if _, err := s.teams.MembershipRole(ctx, teamID, userID); err != nil {
		if errors.Is(err, teams.ErrMembershipNotFound) {
			return ErrTaskNotFound
		}
		return fmt.Errorf("comments service: check membership: %w", err)
	}

	return nil
}
