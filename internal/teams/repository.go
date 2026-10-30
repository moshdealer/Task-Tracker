package teams

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	mysqldriver "github.com/go-sql-driver/mysql"
)

var (
	ErrMembershipNotFound = errors.New("teams: membership not found")
	ErrInviteeNotFound    = errors.New("teams: invitee not found")
	ErrAlreadyMember      = errors.New("teams: user is already a team member")
	ErrOwnerRoleImmutable = errors.New("teams: owner role is immutable")
)

// Repository хранит команды и участников в MySQL
type Repository struct {
	db *sql.DB
}

// NewRepository создаёт репозиторий команд
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// CreateWithOwner атомарно создаёт команду и добавляет создателя с ролью owner
func (r *Repository) CreateWithOwner(ctx context.Context, name string, ownerID uint64) (Team, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Team{}, fmt.Errorf("teams repository: begin create transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	result, err := tx.ExecContext(
		ctx,
		`INSERT INTO teams (name, created_by) VALUES (?, ?)`,
		name,
		ownerID,
	)
	if err != nil {
		return Team{}, fmt.Errorf("teams repository: create team: %w", err)
	}

	teamID, err := result.LastInsertId()
	if err != nil {
		return Team{}, fmt.Errorf("teams repository: get created team id: %w", err)
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO team_members (team_id, user_id, role) VALUES (?, ?, ?)`,
		teamID,
		ownerID,
		RoleOwner,
	); err != nil {
		return Team{}, fmt.Errorf("teams repository: add team owner: %w", err)
	}

	var team Team
	if err := tx.QueryRowContext(
		ctx,
		`SELECT id, name, created_by, created_at FROM teams WHERE id = ?`,
		teamID,
	).Scan(&team.ID, &team.Name, &team.CreatedBy, &team.CreatedAt); err != nil {
		return Team{}, fmt.Errorf("teams repository: get created team: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Team{}, fmt.Errorf("teams repository: commit create transaction: %w", err)
	}

	return team, nil
}

// ListByUser возвращает команды, в которых пользователь является участником
func (r *Repository) ListByUser(ctx context.Context, userID uint64) ([]Team, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT t.id, t.name, t.created_by, t.created_at
		 FROM teams AS t
		 INNER JOIN team_members AS tm ON tm.team_id = t.id
		 WHERE tm.user_id = ?
		 ORDER BY t.id`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("teams repository: list user teams: %w", err)
	}
	defer rows.Close()

	teams := make([]Team, 0)
	for rows.Next() {
		var team Team
		if err := rows.Scan(&team.ID, &team.Name, &team.CreatedBy, &team.CreatedAt); err != nil {
			return nil, fmt.Errorf("teams repository: scan user team: %w", err)
		}
		teams = append(teams, team)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("teams repository: iterate user teams: %w", err)
	}

	return teams, nil
}

// MembershipRole возвращает роль пользователя в команде
func (r *Repository) MembershipRole(ctx context.Context, teamID, userID uint64) (Role, error) {
	var role string
	if err := r.db.QueryRowContext(
		ctx,
		`SELECT role FROM team_members WHERE team_id = ? AND user_id = ?`,
		teamID,
		userID,
	).Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrMembershipNotFound
		}
		return "", fmt.Errorf("teams repository: get membership role: %w", err)
	}

	return Role(role), nil
}

// AddMemberByEmail добавляет существующего пользователя в команду
func (r *Repository) AddMemberByEmail(ctx context.Context, teamID uint64, email string, role Role) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("teams repository: begin invite transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var userID uint64
	if err := tx.QueryRowContext(
		ctx,
		`SELECT id FROM users WHERE email = ?`,
		email,
	).Scan(&userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInviteeNotFound
		}
		return fmt.Errorf("teams repository: find invitee: %w", err)
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO team_members (team_id, user_id, role) VALUES (?, ?, ?)`,
		teamID,
		userID,
		role,
	); err != nil {
		var mysqlErr *mysqldriver.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return ErrAlreadyMember
		}
		return fmt.Errorf("teams repository: add member: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("teams repository: commit invite transaction: %w", err)
	}

	return nil
}

// ChangeMemberRole атомарно меняет роль существующего участника и сохраняет роль owner
func (r *Repository) ChangeMemberRole(ctx context.Context, teamID, userID uint64, role Role) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("teams repository: begin role change transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var currentRole Role
	if err := tx.QueryRowContext(
		ctx,
		`SELECT role
		 FROM team_members
		 WHERE team_id = ? AND user_id = ?
		 FOR UPDATE`,
		teamID,
		userID,
	).Scan(&currentRole); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrMembershipNotFound
		}
		return fmt.Errorf("teams repository: lock member role: %w", err)
	}

	if currentRole == RoleOwner {
		return ErrOwnerRoleImmutable
	}
	if currentRole == role {
		return nil
	}

	if _, err := tx.ExecContext(
		ctx,
		`UPDATE team_members SET role = ? WHERE team_id = ? AND user_id = ?`,
		role,
		teamID,
		userID,
	); err != nil {
		return fmt.Errorf("teams repository: change member role: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("teams repository: commit role change transaction: %w", err)
	}

	return nil
}
