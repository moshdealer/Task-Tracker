//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"task-tracker/internal/analytics"
	"task-tracker/internal/platform/config"
	"task-tracker/internal/platform/database"
	"task-tracker/internal/teams"
	"task-tracker/migrations"
)

func TestTeamStats(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("TEST_DATABASE_DSN"))
	}
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL or TEST_DATABASE_DSN is required")
	}

	databaseConfig := integrationDatabaseConfig(t, dsn)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	db, err := database.Open(ctx, databaseConfig)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close integration database: %v", err)
		}
	})

	if err := database.Migrate(ctx, databaseConfig, migrations.Files); err != nil {
		t.Fatalf("apply integration migrations: %v", err)
	}

	fixture := seedAnalyticsFixture(t, ctx, db)

	teamsService := teams.NewService(teams.NewRepository(db))
	analyticsService := analytics.NewService(analytics.NewRepository(db), teamsService)

	var ownerReport analytics.Stats
	t.Run("owner receives isolated report", func(t *testing.T) {
		ownerReport, err = analyticsService.Stats(ctx, fixture.ownerID, fixture.teamID)
		if err != nil {
			t.Fatalf("get owner report: %v", err)
		}
		assertStats(t, ownerReport, fixture.expected)

		var averageCloseHours any
		if ownerReport.AverageCloseHours != nil {
			averageCloseHours = *ownerReport.AverageCloseHours
		}
		t.Logf(
			"calculated report: team_id=%d tasks_by_status=%+v top_assignees_last_30_days=%+v average_close_hours=%v comments_count=%d",
			ownerReport.TeamID,
			ownerReport.TasksByStatus,
			ownerReport.TopAssigneesLast30Days,
			averageCloseHours,
			ownerReport.CommentsCount,
		)
	})

	t.Run("admin receives the same report", func(t *testing.T) {
		report, err := analyticsService.Stats(ctx, fixture.adminID, fixture.teamID)
		if err != nil {
			t.Fatalf("get admin report: %v", err)
		}
		if !reflect.DeepEqual(report, ownerReport) {
			t.Fatalf("admin report differs from owner report:\nadmin: %#v\nowner: %#v", report, ownerReport)
		}
	})

	t.Run("member is denied", func(t *testing.T) {
		_, err := analyticsService.Stats(ctx, fixture.memberID, fixture.teamID)
		if !errors.Is(err, analytics.ErrAccessDenied) {
			t.Fatalf("member error = %v, want ErrAccessDenied", err)
		}
	})

	t.Run("outsider does not discover the team", func(t *testing.T) {
		_, err := analyticsService.Stats(ctx, fixture.outsiderID, fixture.teamID)
		if !errors.Is(err, analytics.ErrTeamNotFound) {
			t.Fatalf("outsider error = %v, want ErrTeamNotFound", err)
		}
	})

	t.Run("missing team is indistinguishable from foreign team", func(t *testing.T) {
		_, err := analyticsService.Stats(ctx, fixture.ownerID, ^uint64(0))
		if !errors.Is(err, analytics.ErrTeamNotFound) {
			t.Fatalf("missing team error = %v, want ErrTeamNotFound", err)
		}
	})

	t.Run("empty team returns zero metrics", func(t *testing.T) {
		report, err := analyticsService.Stats(ctx, fixture.ownerID, fixture.emptyTeamID)
		if err != nil {
			t.Fatalf("get empty team report: %v", err)
		}
		if report.TeamID != fixture.emptyTeamID {
			t.Fatalf("empty team id = %d, want %d", report.TeamID, fixture.emptyTeamID)
		}
		if report.TasksByStatus != (analytics.StatusCounts{}) {
			t.Fatalf("empty status counts = %#v, want zeros", report.TasksByStatus)
		}
		if len(report.TopAssigneesLast30Days) != 0 {
			t.Fatalf("empty top assignees = %#v, want []", report.TopAssigneesLast30Days)
		}
		if report.AverageCloseHours != nil {
			t.Fatalf("empty average = %v, want nil", *report.AverageCloseHours)
		}
		if report.CommentsCount != 0 {
			t.Fatalf("empty comments count = %d, want 0", report.CommentsCount)
		}
	})
}

type analyticsFixture struct {
	teamID      uint64
	emptyTeamID uint64
	ownerID     uint64
	adminID     uint64
	memberID    uint64
	outsiderID  uint64
	teamIDs     []uint64
	userIDs     []uint64
	expected    analytics.Stats
}

func integrationDatabaseConfig(t *testing.T, dsn string) config.Database {
	t.Helper()

	driverConfig, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse integration database DSN: %v", err)
	}
	if driverConfig.DBName == "" {
		t.Fatal("integration database DSN must contain a database name")
	}
	driverConfig.ParseTime = true
	driverConfig.Loc = time.UTC

	return config.Database{
		URL:             driverConfig.FormatDSN(),
		MaxOpenConns:    5,
		MaxIdleConns:    2,
		ConnMaxLifetime: time.Minute,
	}
}

func seedAnalyticsFixture(t *testing.T, ctx context.Context, db *sql.DB) analyticsFixture {
	t.Helper()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin analytics fixture transaction: %v", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var databaseNow time.Time
	if err := tx.QueryRowContext(ctx, `SELECT UTC_TIMESTAMP(6)`).Scan(&databaseNow); err != nil {
		t.Fatalf("read MySQL UTC time: %v", err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	ownerID := insertUser(t, ctx, tx, suffix, "owner", "Владелец A")
	adminID := insertUser(t, ctx, tx, suffix, "admin", "Администратор A")
	memberID := insertUser(t, ctx, tx, suffix, "member", "Участник A")
	worker1ID := insertUser(t, ctx, tx, suffix, "worker1", "Исполнитель 1")
	worker2ID := insertUser(t, ctx, tx, suffix, "worker2", "Исполнитель 2")
	worker3ID := insertUser(t, ctx, tx, suffix, "worker3", "Исполнитель 3")
	worker4ID := insertUser(t, ctx, tx, suffix, "worker4", "Исполнитель 4")
	outsiderID := insertUser(t, ctx, tx, suffix, "outsider", "Владелец B")

	teamID := insertTeam(t, ctx, tx, "Analytics A "+suffix, ownerID)
	foreignTeamID := insertTeam(t, ctx, tx, "Analytics B "+suffix, outsiderID)
	emptyTeamID := insertTeam(t, ctx, tx, "Analytics Empty "+suffix, ownerID)

	insertMembership(t, ctx, tx, teamID, ownerID, "owner")
	insertMembership(t, ctx, tx, teamID, adminID, "admin")
	insertMembership(t, ctx, tx, teamID, memberID, "member")
	insertMembership(t, ctx, tx, teamID, worker1ID, "member")
	insertMembership(t, ctx, tx, teamID, worker2ID, "member")
	insertMembership(t, ctx, tx, teamID, worker3ID, "member")
	insertMembership(t, ctx, tx, teamID, worker4ID, "member")
	insertMembership(t, ctx, tx, foreignTeamID, outsiderID, "owner")
	insertMembership(t, ctx, tx, emptyTeamID, ownerID, "owner")

	closedTask := func(title string, assigneeID *uint64, status string, closedAt time.Time, duration time.Duration) uint64 {
		return insertTask(t, ctx, tx, teamID, title+" "+suffix, status, ownerID, assigneeID, closedAt.Add(-duration), &closedAt)
	}

	d1 := closedTask("D1", &worker1ID, "done", databaseNow.Add(-24*time.Hour), 2*time.Hour)
	closedTask("D2", &worker1ID, "done", databaseNow.Add(-2*24*time.Hour), 4*time.Hour)
	closedTask("D3", &worker1ID, "done", databaseNow.Add(-3*24*time.Hour), 6*time.Hour)
	d4 := closedTask("D4", &worker2ID, "done", databaseNow.Add(-4*24*time.Hour), 8*time.Hour)
	closedTask("D5", &worker2ID, "done", databaseNow.Add(-5*24*time.Hour), 10*time.Hour)
	closedTask("D6", &worker3ID, "done", databaseNow.Add(-30*24*time.Hour+time.Hour), 12*time.Hour)
	closedTask("D7", &worker4ID, "done", databaseNow.Add(-10*24*time.Hour), 14*time.Hour)
	closedTask("D8", &worker4ID, "done", databaseNow.Add(-30*24*time.Hour-time.Hour), 16*time.Hour)
	closedTask("D9", nil, "done", databaseNow.Add(-6*24*time.Hour), 18*time.Hour)
	cancelTaskID := closedTask("C1", &worker2ID, "cancel", databaseNow.Add(-7*24*time.Hour), 20*time.Hour)

	openTaskID := insertTask(t, ctx, tx, teamID, "Todo "+suffix, "todo", ownerID, nil, databaseNow.Add(-time.Hour), nil)
	insertTask(t, ctx, tx, teamID, "Reopened "+suffix, "todo", ownerID, &worker1ID, databaseNow.Add(-20*24*time.Hour), nil)
	insertTask(t, ctx, tx, teamID, "In progress "+suffix, "in_progress", ownerID, &worker2ID, databaseNow.Add(-2*time.Hour), nil)
	insertTask(t, ctx, tx, teamID, "Hold "+suffix, "hold", ownerID, &worker3ID, databaseNow.Add(-3*time.Hour), nil)

	insertComment(t, ctx, tx, d1, memberID, "D1-1 "+suffix)
	insertComment(t, ctx, tx, d1, memberID, "D1-2 "+suffix)
	insertComment(t, ctx, tx, d1, memberID, "D1-3 "+suffix)
	insertComment(t, ctx, tx, d4, adminID, "D4 "+suffix)
	insertComment(t, ctx, tx, openTaskID, memberID, "Todo "+suffix)
	insertComment(t, ctx, tx, cancelTaskID, memberID, "Cancel "+suffix)

	for index := 0; index < 5; index++ {
		closedAt := databaseNow.Add(-time.Duration(index+1) * time.Hour)
		foreignTaskID := insertTask(
			t,
			ctx,
			tx,
			foreignTeamID,
			fmt.Sprintf("Foreign %d %s", index, suffix),
			"done",
			outsiderID,
			&outsiderID,
			closedAt.Add(-100*time.Hour),
			&closedAt,
		)
		insertComment(t, ctx, tx, foreignTaskID, outsiderID, fmt.Sprintf("Foreign comment %d %s", index, suffix))
	}

	average := 10.0
	fixture := analyticsFixture{
		teamID:      teamID,
		emptyTeamID: emptyTeamID,
		ownerID:     ownerID,
		adminID:     adminID,
		memberID:    memberID,
		outsiderID:  outsiderID,
		teamIDs:     []uint64{teamID, foreignTeamID, emptyTeamID},
		userIDs: []uint64{
			ownerID,
			adminID,
			memberID,
			worker1ID,
			worker2ID,
			worker3ID,
			worker4ID,
			outsiderID,
		},
		expected: analytics.Stats{
			TeamID: teamID,
			TasksByStatus: analytics.StatusCounts{
				Todo:       2,
				InProgress: 1,
				Hold:       1,
				Done:       9,
				Cancel:     1,
			},
			TopAssigneesLast30Days: []analytics.TopAssignee{
				{AssigneeID: worker1ID, Name: "Исполнитель 1", ClosedTasks: 3},
				{AssigneeID: worker2ID, Name: "Исполнитель 2", ClosedTasks: 2},
				{AssigneeID: worker3ID, Name: "Исполнитель 3", ClosedTasks: 1},
			},
			AverageCloseHours: &average,
			CommentsCount:     6,
		},
	}
	t.Cleanup(func() {
		cleanupFixture(t, db, fixture.teamIDs, fixture.userIDs)
	})

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit analytics fixture: %v", err)
	}

	return fixture
}

func insertUser(t *testing.T, ctx context.Context, tx *sql.Tx, suffix, role, name string) uint64 {
	t.Helper()

	result, err := tx.ExecContext(
		ctx,
		`INSERT INTO users (email, password_hash, name) VALUES (?, ?, ?)`,
		fmt.Sprintf("analytics-%s-%s@example.test", suffix, role),
		"integration-test",
		name,
	)
	if err != nil {
		t.Fatalf("insert %s user: %v", role, err)
	}
	return insertedID(t, result, "user")
}

func insertTeam(t *testing.T, ctx context.Context, tx *sql.Tx, name string, creatorID uint64) uint64 {
	t.Helper()

	result, err := tx.ExecContext(ctx, `INSERT INTO teams (name, created_by) VALUES (?, ?)`, name, creatorID)
	if err != nil {
		t.Fatalf("insert team %q: %v", name, err)
	}
	return insertedID(t, result, "team")
}

func insertMembership(t *testing.T, ctx context.Context, tx *sql.Tx, teamID, userID uint64, role string) {
	t.Helper()

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO team_members (team_id, user_id, role) VALUES (?, ?, ?)`,
		teamID,
		userID,
		role,
	); err != nil {
		t.Fatalf("insert %s membership for team %d and user %d: %v", role, teamID, userID, err)
	}
}

func insertTask(
	t *testing.T,
	ctx context.Context,
	tx *sql.Tx,
	teamID uint64,
	title, status string,
	creatorID uint64,
	assigneeID *uint64,
	createdAt time.Time,
	closedAt *time.Time,
) uint64 {
	t.Helper()

	result, err := tx.ExecContext(
		ctx,
		`INSERT INTO tasks
			(team_id, title, description, status, created_by, assignee_id, created_at, updated_at, closed_at, version)
		 VALUES (?, ?, NULL, ?, ?, ?, ?, ?, ?, 1)`,
		teamID,
		title,
		status,
		creatorID,
		nullableUint64(assigneeID),
		createdAt,
		createdAt,
		nullableTime(closedAt),
	)
	if err != nil {
		t.Fatalf("insert task %q: %v", title, err)
	}
	return insertedID(t, result, "task")
}

func insertComment(t *testing.T, ctx context.Context, tx *sql.Tx, taskID, userID uint64, content string) {
	t.Helper()

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO task_comments (task_id, user_id, content) VALUES (?, ?, ?)`,
		taskID,
		userID,
		content,
	); err != nil {
		t.Fatalf("insert comment for task %d: %v", taskID, err)
	}
}

func insertedID(t *testing.T, result sql.Result, entity string) uint64 {
	t.Helper()

	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read inserted %s id: %v", entity, err)
	}
	return uint64(id)
}

func nullableUint64(value *uint64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

func assertStats(t *testing.T, actual, expected analytics.Stats) {
	t.Helper()

	if actual.TeamID != expected.TeamID {
		t.Errorf("team id = %d, want %d", actual.TeamID, expected.TeamID)
	}
	if actual.TasksByStatus != expected.TasksByStatus {
		t.Errorf("status counts = %#v, want %#v", actual.TasksByStatus, expected.TasksByStatus)
	}
	if !reflect.DeepEqual(actual.TopAssigneesLast30Days, expected.TopAssigneesLast30Days) {
		t.Errorf("top assignees = %#v, want %#v", actual.TopAssigneesLast30Days, expected.TopAssigneesLast30Days)
	}
	if actual.AverageCloseHours == nil || expected.AverageCloseHours == nil {
		if actual.AverageCloseHours != expected.AverageCloseHours {
			t.Errorf("average close hours = %v, want %v", actual.AverageCloseHours, expected.AverageCloseHours)
		}
	} else if math.Abs(*actual.AverageCloseHours-*expected.AverageCloseHours) > 0.000001 {
		t.Errorf("average close hours = %f, want %f", *actual.AverageCloseHours, *expected.AverageCloseHours)
	}
	if actual.CommentsCount != expected.CommentsCount {
		t.Errorf("comments count = %d, want %d", actual.CommentsCount, expected.CommentsCount)
	}
}

func cleanupFixture(t *testing.T, db *sql.DB, teamIDs, userIDs []uint64) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	teamPlaceholders := placeholders(len(teamIDs))
	userPlaceholders := placeholders(len(userIDs))
	teamArguments := uint64Arguments(teamIDs)
	userArguments := uint64Arguments(userIDs)

	queries := []struct {
		query string
		args  []any
	}{
		{
			query: `DELETE FROM task_comments WHERE task_id IN (
				SELECT id FROM tasks WHERE team_id IN (` + teamPlaceholders + `)
			)`,
			args: teamArguments,
		},
		{
			query: `DELETE FROM task_history WHERE task_id IN (
				SELECT id FROM tasks WHERE team_id IN (` + teamPlaceholders + `)
			)`,
			args: teamArguments,
		},
		{query: `DELETE FROM tasks WHERE team_id IN (` + teamPlaceholders + `)`, args: teamArguments},
		{query: `DELETE FROM team_members WHERE team_id IN (` + teamPlaceholders + `)`, args: teamArguments},
		{query: `DELETE FROM teams WHERE id IN (` + teamPlaceholders + `)`, args: teamArguments},
		{query: `DELETE FROM users WHERE id IN (` + userPlaceholders + `)`, args: userArguments},
	}

	for _, statement := range queries {
		if _, err := db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Errorf("cleanup analytics fixture: %v", err)
		}
	}
}

func placeholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func uint64Arguments(values []uint64) []any {
	arguments := make([]any, len(values))
	for index, value := range values {
		arguments[index] = value
	}
	return arguments
}
