package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrTaskNotFound    = errors.New("tasks: task not found")
	ErrVersionConflict = errors.New("tasks: version conflict")
)

// Repository хранит задачи и их историю в MySQL
type Repository struct {
	db *sql.DB
}

// NewRepository создаёт репозиторий задач
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// Create атомарно создаёт задачу и начальную запись истории
func (r *Repository) Create(
	ctx context.Context,
	creatorID uint64,
	input CreateInput,
	closedAt *time.Time,
) (Task, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, fmt.Errorf("tasks repository: begin create transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	result, err := tx.ExecContext(
		ctx,
		`INSERT INTO tasks
			(team_id, title, description, status, created_by, assignee_id, closed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		input.TeamID,
		input.Title,
		input.Description,
		input.Status,
		creatorID,
		input.AssigneeID,
		closedAt,
	)
	if err != nil {
		return Task{}, fmt.Errorf("tasks repository: create task: %w", err)
	}

	taskID, err := result.LastInsertId()
	if err != nil {
		return Task{}, fmt.Errorf("tasks repository: get created task id: %w", err)
	}

	task, err := getTask(ctx, tx, uint64(taskID), false)
	if err != nil {
		return Task{}, fmt.Errorf("tasks repository: get created task: %w", err)
	}

	changes := taskChanges{
		"team_id":    {Old: nil, New: task.TeamID},
		"title":      {Old: nil, New: task.Title},
		"status":     {Old: nil, New: task.Status},
		"created_by": {Old: nil, New: task.CreatedBy},
	}
	if task.Description != nil {
		changes["description"] = fieldChange{Old: nil, New: task.Description}
	}
	if task.AssigneeID != nil {
		changes["assignee_id"] = fieldChange{Old: nil, New: task.AssigneeID}
	}
	if task.ClosedAt != nil {
		changes["closed_at"] = fieldChange{Old: nil, New: task.ClosedAt}
	}
	if err := insertHistory(ctx, tx, task.ID, creatorID, changes); err != nil {
		return Task{}, err
	}

	if err := tx.Commit(); err != nil {
		return Task{}, fmt.Errorf("tasks repository: commit create transaction: %w", err)
	}

	return task, nil
}

// GetByID возвращает задачу по идентификатору
func (r *Repository) GetByID(ctx context.Context, taskID uint64) (Task, error) {
	task, err := getTask(ctx, r.db, taskID, false)
	if err != nil {
		return Task{}, fmt.Errorf("tasks repository: get task: %w", err)
	}
	return task, nil
}

// List возвращает список задач с фильтрами и пагинацией
func (r *Repository) List(ctx context.Context, filter ListFilter) ([]Task, error) {
	var query strings.Builder
	query.WriteString(
		`SELECT id, team_id, title, description, status, created_by, assignee_id,
		        created_at, updated_at, closed_at, version
		 FROM tasks
		 WHERE team_id = ?`,
	)
	args := []any{filter.TeamID}

	if filter.Status != nil {
		query.WriteString(" AND status = ?")
		args = append(args, *filter.Status)
	}
	if filter.AssigneeID != nil {
		query.WriteString(" AND assignee_id = ?")
		args = append(args, *filter.AssigneeID)
	}
	query.WriteString(" ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?")
	args = append(args, filter.Limit, filter.Offset)

	rows, err := r.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("tasks repository: list tasks: %w", err)
	}
	defer rows.Close()

	result := make([]Task, 0)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("tasks repository: scan listed task: %w", err)
		}
		result = append(result, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tasks repository: iterate tasks: %w", err)
	}

	return result, nil
}

// Update атомарно обновляет задачу и записывает фактические изменения
func (r *Repository) Update(
	ctx context.Context,
	taskID, changedBy, expectedVersion uint64,
	input UpdateInput,
) (Task, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, fmt.Errorf("tasks repository: begin update transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	current, err := getTask(ctx, tx, taskID, true)
	if err != nil {
		return Task{}, fmt.Errorf("tasks repository: lock task: %w", err)
	}
	if current.Version != expectedVersion {
		return Task{}, ErrVersionConflict
	}

	updated, changes := applyUpdate(current, input, time.Now().UTC())
	if len(changes) == 0 {
		return current, nil
	}

	result, err := tx.ExecContext(
		ctx,
		`UPDATE tasks
		 SET title = ?, description = ?, status = ?, assignee_id = ?, closed_at = ?,
		     version = version + 1
		 WHERE id = ? AND version = ?`,
		updated.Title,
		updated.Description,
		updated.Status,
		updated.AssigneeID,
		updated.ClosedAt,
		taskID,
		expectedVersion,
	)
	if err != nil {
		return Task{}, fmt.Errorf("tasks repository: update task: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return Task{}, fmt.Errorf("tasks repository: get updated rows: %w", err)
	}
	if rowsAffected != 1 {
		return Task{}, ErrVersionConflict
	}

	if err := insertHistory(ctx, tx, taskID, changedBy, changes); err != nil {
		return Task{}, err
	}

	updated, err = getTask(ctx, tx, taskID, false)
	if err != nil {
		return Task{}, fmt.Errorf("tasks repository: get updated task: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Task{}, fmt.Errorf("tasks repository: commit update transaction: %w", err)
	}

	return updated, nil
}

// HistoryByTask возвращает историю задачи от старых записей к новым
func (r *Repository) HistoryByTask(ctx context.Context, taskID uint64) ([]HistoryEntry, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, task_id, changed_by, changes, created_at
		 FROM task_history
		 WHERE task_id = ?
		 ORDER BY created_at ASC, id ASC`,
		taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("tasks repository: list task history: %w", err)
	}
	defer rows.Close()

	result := make([]HistoryEntry, 0)
	for rows.Next() {
		var entry HistoryEntry
		var changes []byte
		if err := rows.Scan(
			&entry.ID,
			&entry.TaskID,
			&entry.ChangedBy,
			&changes,
			&entry.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("tasks repository: scan task history: %w", err)
		}
		entry.Changes = append(json.RawMessage(nil), changes...)
		result = append(result, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tasks repository: iterate task history: %w", err)
	}

	return result, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

type taskQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func getTask(ctx context.Context, queryer taskQueryer, taskID uint64, forUpdate bool) (Task, error) {
	query := `SELECT id, team_id, title, description, status, created_by, assignee_id,
	                 created_at, updated_at, closed_at, version
	          FROM tasks
	          WHERE id = ?`
	if forUpdate {
		query += " FOR UPDATE"
	}

	task, err := scanTask(queryer.QueryRowContext(ctx, query, taskID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, ErrTaskNotFound
		}
		return Task{}, err
	}
	return task, nil
}

func scanTask(row rowScanner) (Task, error) {
	var task Task
	var description sql.Null[string]
	var assigneeID sql.Null[uint64]
	var closedAt sql.Null[time.Time]

	if err := row.Scan(
		&task.ID,
		&task.TeamID,
		&task.Title,
		&description,
		&task.Status,
		&task.CreatedBy,
		&assigneeID,
		&task.CreatedAt,
		&task.UpdatedAt,
		&closedAt,
		&task.Version,
	); err != nil {
		return Task{}, err
	}

	if description.Valid {
		task.Description = &description.V
	}
	if assigneeID.Valid {
		task.AssigneeID = &assigneeID.V
	}
	if closedAt.Valid {
		task.ClosedAt = &closedAt.V
	}

	return task, nil
}

func insertHistory(
	ctx context.Context,
	tx *sql.Tx,
	taskID, changedBy uint64,
	changes taskChanges,
) error {
	encodedChanges, err := json.Marshal(changes)
	if err != nil {
		return fmt.Errorf("tasks repository: encode history changes: %w", err)
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO task_history (task_id, changed_by, changes) VALUES (?, ?, ?)`,
		taskID,
		changedBy,
		encodedChanges,
	); err != nil {
		return fmt.Errorf("tasks repository: create history entry: %w", err)
	}
	return nil
}

func applyUpdate(current Task, input UpdateInput, now time.Time) (Task, taskChanges) {
	updated := current
	changes := make(taskChanges)

	if input.TitleSet && input.Title != current.Title {
		changes["title"] = fieldChange{Old: current.Title, New: input.Title}
		updated.Title = input.Title
	}
	if input.DescriptionSet && !equalStrings(input.Description, current.Description) {
		changes["description"] = fieldChange{Old: current.Description, New: input.Description}
		updated.Description = input.Description
	}
	if input.AssigneeIDSet && !equalUint64s(input.AssigneeID, current.AssigneeID) {
		changes["assignee_id"] = fieldChange{Old: current.AssigneeID, New: input.AssigneeID}
		updated.AssigneeID = input.AssigneeID
	}
	if input.StatusSet && input.Status != current.Status {
		changes["status"] = fieldChange{Old: current.Status, New: input.Status}
		updated.Status = input.Status

		var closedAt *time.Time
		if terminalStatus(input.Status) {
			closedAt = &now
		}
		if !equalTimes(closedAt, current.ClosedAt) {
			changes["closed_at"] = fieldChange{Old: current.ClosedAt, New: closedAt}
			updated.ClosedAt = closedAt
		}
	}

	return updated, changes
}

func equalStrings(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func equalUint64s(left, right *uint64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func equalTimes(left, right *time.Time) bool {
	return left == nil && right == nil || left != nil && right != nil && left.Equal(*right)
}
