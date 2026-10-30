package analytics

import (
	"context"
	"database/sql"
	"fmt"
)

const teamStatsQuery = `
WITH team_tasks AS (
	SELECT id, status, assignee_id, created_at, closed_at
	FROM tasks
	WHERE team_id = ?
),
summary AS (
	SELECT
		COALESCE(SUM(status = 'todo'), 0) AS todo_count,
		COALESCE(SUM(status = 'in_progress'), 0) AS in_progress_count,
		COALESCE(SUM(status = 'hold'), 0) AS hold_count,
		COALESCE(SUM(status = 'done'), 0) AS done_count,
		COALESCE(SUM(status = 'cancel'), 0) AS cancel_count,
		AVG(
			CASE
				WHEN status = 'done' AND closed_at IS NOT NULL
				THEN TIMESTAMPDIFF(MICROSECOND, created_at, closed_at) / 3600000000.0
			END
		) AS average_close_hours
	FROM team_tasks
),
comment_summary AS (
	SELECT COUNT(tc.id) AS comments_count
	FROM team_tasks AS tt
	INNER JOIN task_comments AS tc ON tc.task_id = tt.id
),
assignee_counts AS (
	SELECT tt.assignee_id, COUNT(*) AS closed_tasks
	FROM team_tasks AS tt
	WHERE tt.status = 'done'
		AND tt.closed_at IS NOT NULL
		AND tt.closed_at >= UTC_TIMESTAMP(6) - INTERVAL 30 DAY
		AND tt.closed_at <= UTC_TIMESTAMP(6)
		AND tt.assignee_id IS NOT NULL
	GROUP BY tt.assignee_id
),
ranked_assignees AS (
	SELECT
		ac.assignee_id,
		u.name,
		ac.closed_tasks,
		ROW_NUMBER() OVER (
			ORDER BY ac.closed_tasks DESC, ac.assignee_id ASC
		) AS position
	FROM assignee_counts AS ac
	INNER JOIN users AS u ON u.id = ac.assignee_id
)
SELECT
	s.todo_count,
	s.in_progress_count,
	s.hold_count,
	s.done_count,
	s.cancel_count,
	s.average_close_hours,
	cs.comments_count,
	ra.assignee_id,
	ra.name,
	ra.closed_tasks
FROM summary AS s
CROSS JOIN comment_summary AS cs
LEFT JOIN ranked_assignees AS ra ON ra.position <= 3
ORDER BY ra.position`

// Repository выполняет SQL-отчёт по данным одной команды
type Repository struct {
	db *sql.DB
}

// NewRepository создаёт репозиторий аналитики
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// Stats возвращает агрегированные метрики одной команды
func (r *Repository) Stats(ctx context.Context, teamID uint64) (Stats, error) {
	rows, err := r.db.QueryContext(ctx, teamStatsQuery, teamID)
	if err != nil {
		return Stats{}, fmt.Errorf("analytics repository: query team stats: %w", err)
	}
	defer rows.Close()

	report := Stats{
		TeamID:                 teamID,
		TopAssigneesLast30Days: make([]TopAssignee, 0, 3),
	}
	rowFound := false
	for rows.Next() {
		var averageCloseHours sql.Null[float64]
		var assigneeID sql.Null[uint64]
		var assigneeName sql.Null[string]
		var closedTasks sql.Null[uint64]

		if err := rows.Scan(
			&report.TasksByStatus.Todo,
			&report.TasksByStatus.InProgress,
			&report.TasksByStatus.Hold,
			&report.TasksByStatus.Done,
			&report.TasksByStatus.Cancel,
			&averageCloseHours,
			&report.CommentsCount,
			&assigneeID,
			&assigneeName,
			&closedTasks,
		); err != nil {
			return Stats{}, fmt.Errorf("analytics repository: scan team stats: %w", err)
		}
		rowFound = true

		if averageCloseHours.Valid {
			value := averageCloseHours.V
			report.AverageCloseHours = &value
		}
		if assigneeID.Valid && assigneeName.Valid && closedTasks.Valid {
			report.TopAssigneesLast30Days = append(report.TopAssigneesLast30Days, TopAssignee{
				AssigneeID:  assigneeID.V,
				Name:        assigneeName.V,
				ClosedTasks: closedTasks.V,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return Stats{}, fmt.Errorf("analytics repository: iterate team stats: %w", err)
	}
	if !rowFound {
		return Stats{}, fmt.Errorf("analytics repository: query returned no summary row")
	}

	return report, nil
}
