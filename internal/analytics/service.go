package analytics

import (
	"context"
	"errors"
	"fmt"

	"task-tracker/internal/teams"
)

var (
	ErrInvalidTeamID = errors.New("analytics: invalid team id")
	ErrTeamNotFound  = errors.New("analytics: team not found")
	ErrAccessDenied  = errors.New("analytics: access denied")
)

type statsRepository interface {
	Stats(ctx context.Context, teamID uint64) (Stats, error)
}

type teamAccess interface {
	MembershipRole(ctx context.Context, teamID, userID uint64) (teams.Role, error)
}

// Service реализует сценарий получения аналитики команды
type Service struct {
	repository statsRepository
	teams      teamAccess
}

// NewService создаёт сервис аналитики
func NewService(repository statsRepository, teams teamAccess) *Service {
	return &Service{
		repository: repository,
		teams:      teams,
	}
}

// Stats проверяет доступ и возвращает SQL-отчёт команды
func (s *Service) Stats(ctx context.Context, userID, teamID uint64) (Stats, error) {
	if teamID == 0 {
		return Stats{}, ErrInvalidTeamID
	}

	role, err := s.teams.MembershipRole(ctx, teamID, userID)
	if err != nil {
		if errors.Is(err, teams.ErrMembershipNotFound) {
			return Stats{}, ErrTeamNotFound
		}
		return Stats{}, fmt.Errorf("analytics service: get membership role: %w", err)
	}
	if role != teams.RoleOwner && role != teams.RoleAdmin {
		return Stats{}, ErrAccessDenied
	}

	report, err := s.repository.Stats(ctx, teamID)
	if err != nil {
		return Stats{}, fmt.Errorf("analytics service: get team stats: %w", err)
	}
	return report, nil
}
