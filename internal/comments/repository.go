package comments

import (
	"context"
	"database/sql"
	"fmt"
)

// Repository хранит комментарии к задачам в MySQL
type Repository struct {
	db *sql.DB
}

// NewRepository создаёт репозиторий комментариев
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// Create создаёт комментарий и возвращает сохранённые данные
func (r *Repository) Create(ctx context.Context, taskID, userID uint64, content string) (Comment, error) {
	result, err := r.db.ExecContext(
		ctx,
		`INSERT INTO task_comments (task_id, user_id, content) VALUES (?, ?, ?)`,
		taskID,
		userID,
		content,
	)
	if err != nil {
		return Comment{}, fmt.Errorf("comments repository: create comment: %w", err)
	}

	commentID, err := result.LastInsertId()
	if err != nil {
		return Comment{}, fmt.Errorf("comments repository: get created comment id: %w", err)
	}

	comment, err := r.getByID(ctx, uint64(commentID))
	if err != nil {
		return Comment{}, fmt.Errorf("comments repository: get created comment: %w", err)
	}

	return comment, nil
}

// ListByTask возвращает комментарии задачи от старых к новым
func (r *Repository) ListByTask(ctx context.Context, taskID uint64) ([]Comment, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, task_id, user_id, content, created_at
		 FROM task_comments
		 WHERE task_id = ?
		 ORDER BY created_at ASC, id ASC`,
		taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("comments repository: list task comments: %w", err)
	}
	defer rows.Close()

	result := make([]Comment, 0)
	for rows.Next() {
		var comment Comment
		if err := rows.Scan(
			&comment.ID,
			&comment.TaskID,
			&comment.UserID,
			&comment.Content,
			&comment.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("comments repository: scan task comment: %w", err)
		}
		result = append(result, comment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("comments repository: iterate task comments: %w", err)
	}

	return result, nil
}

func (r *Repository) getByID(ctx context.Context, commentID uint64) (Comment, error) {
	var comment Comment
	if err := r.db.QueryRowContext(
		ctx,
		`SELECT id, task_id, user_id, content, created_at
		 FROM task_comments
		 WHERE id = ?`,
		commentID,
	).Scan(
		&comment.ID,
		&comment.TaskID,
		&comment.UserID,
		&comment.Content,
		&comment.CreatedAt,
	); err != nil {
		return Comment{}, err
	}

	return comment, nil
}
