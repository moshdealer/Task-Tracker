package teams

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
)

var (
	ErrInvalidTeamName    = errors.New("teams: invalid team name")
	ErrInvalidInviteEmail = errors.New("teams: invalid invite email")
	ErrInvalidInviteRole  = errors.New("teams: invalid invite role")
	ErrInvalidTeamID      = errors.New("teams: invalid team id")
	ErrInvalidMemberID    = errors.New("teams: invalid member id")
	ErrInvalidMemberRole  = errors.New("teams: invalid member role")
	ErrAccessDenied       = errors.New("teams: access denied")
)

type teamRepository interface {
	CreateWithOwner(ctx context.Context, name string, ownerID uint64) (Team, error)
	ListByUser(ctx context.Context, userID uint64) ([]Team, error)
	MembershipRole(ctx context.Context, teamID, userID uint64) (Role, error)
	AddMemberByEmail(ctx context.Context, teamID uint64, email string, role Role) error
	ChangeMemberRole(ctx context.Context, teamID, userID uint64, role Role) error
}

// Service реализует сценарии работы с командами
type Service struct {
	teams teamRepository
}

// NewService создаёт сервис команд
func NewService(teams teamRepository) *Service {
	return &Service{teams: teams}
}

// Create создаёт команду и назначает текущего пользователя её создателем
func (s *Service) Create(ctx context.Context, userID uint64, input CreateInput) (Team, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 255 {
		return Team{}, ErrInvalidTeamName
	}

	team, err := s.teams.CreateWithOwner(ctx, name, userID)
	if err != nil {
		return Team{}, fmt.Errorf("teams service: create team: %w", err)
	}

	return team, nil
}

// List возвращает команды текущего пользователя
func (s *Service) List(ctx context.Context, userID uint64) ([]Team, error) {
	teams, err := s.teams.ListByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("teams service: list teams: %w", err)
	}

	return teams, nil
}

// MembershipRole возвращает роль пользователя в команде
func (s *Service) MembershipRole(ctx context.Context, teamID, userID uint64) (Role, error) {
	role, err := s.teams.MembershipRole(ctx, teamID, userID)
	if err != nil {
		return "", fmt.Errorf("teams service: get membership role: %w", err)
	}

	return role, nil
}

// Invite добавляет существующего пользователя в команду с разрешённой ролью
func (s *Service) Invite(ctx context.Context, actorID, teamID uint64, input InviteInput) error {
	email, err := normalizeInviteEmail(input.Email)
	if err != nil {
		return err
	}
	if input.Role != RoleMember && input.Role != RoleAdmin {
		return ErrInvalidInviteRole
	}

	actorRole, err := s.teams.MembershipRole(ctx, teamID, actorID)
	if err != nil {
		if errors.Is(err, ErrMembershipNotFound) {
			return ErrAccessDenied
		}
		return fmt.Errorf("teams service: get actor role: %w", err)
	}

	if actorRole != RoleOwner && actorRole != RoleAdmin {
		return ErrAccessDenied
	}
	if actorRole == RoleAdmin && input.Role != RoleMember {
		return ErrAccessDenied
	}

	if err := s.teams.AddMemberByEmail(ctx, teamID, email, input.Role); err != nil {
		return fmt.Errorf("teams service: invite member: %w", err)
	}

	return nil
}

// ChangeMemberRole меняет роль существующего участника от имени владельца команды
func (s *Service) ChangeMemberRole(
	ctx context.Context,
	actorID, teamID, memberID uint64,
	input ChangeMemberRoleInput,
) error {
	if teamID == 0 {
		return ErrInvalidTeamID
	}
	if memberID == 0 {
		return ErrInvalidMemberID
	}
	if input.Role != RoleMember && input.Role != RoleAdmin {
		return ErrInvalidMemberRole
	}

	actorRole, err := s.teams.MembershipRole(ctx, teamID, actorID)
	if err != nil {
		if errors.Is(err, ErrMembershipNotFound) {
			return ErrAccessDenied
		}
		return fmt.Errorf("teams service: get role change actor role: %w", err)
	}
	if actorRole != RoleOwner {
		return ErrAccessDenied
	}

	if err := s.teams.ChangeMemberRole(ctx, teamID, memberID, input.Role); err != nil {
		return fmt.Errorf("teams service: change member role: %w", err)
	}

	return nil
}

func normalizeInviteEmail(value string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return "", ErrInvalidInviteEmail
	}

	return email, nil
}
