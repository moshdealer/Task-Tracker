package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"task-tracker/internal/analytics"
	"task-tracker/internal/auth"
	"task-tracker/internal/comments"
	platformcache "task-tracker/internal/platform/cache"
	"task-tracker/internal/platform/config"
	"task-tracker/internal/platform/database"
	"task-tracker/internal/platform/httpserver"
	"task-tracker/internal/platform/jwt"
	"task-tracker/internal/platform/logger"
	"task-tracker/internal/tasks"
	"task-tracker/internal/teams"
	"task-tracker/migrations"
)

// App объединяет зависимости приложения и управляет их жизненным циклом
type App struct {
	config config.Config
	db     *sql.DB
	cache  *platformcache.Redis
	server *httpserver.Server
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		slog.Error("application stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) (runErr error) {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config load error: %w", err)
	}
	slog.SetDefault(logger.New(cfg.Log))

	app, err := NewApp(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		if err := app.Close(); err != nil && runErr == nil {
			runErr = err
		}
	}()

	return app.Run(ctx)
}

// NewApp создаёт приложение и связывает его инфраструктурные зависимости
func NewApp(ctx context.Context, cfg config.Config) (*App, error) {
	db, err := database.Open(ctx, cfg.Database)
	if err != nil {
		return nil, err
	}
	slog.Info("connected to MySQL")

	if err := database.Migrate(ctx, cfg.Database, migrations.Files); err != nil {
		_ = db.Close()
		return nil, err
	}
	slog.Info("database migrations are up to date")

	redisCache, err := platformcache.NewRedis(ctx, cfg.Redis)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	slog.Info("connected to Redis")

	tokenManager := jwt.New(cfg.JWT)
	authRepository := auth.NewRepository(db)
	authService := auth.NewService(authRepository, tokenManager)
	authHandler := auth.NewHandler(authService)
	authMiddleware := auth.NewMiddleware(tokenManager)

	teamsRepository := teams.NewRepository(db)
	teamsService := teams.NewService(teamsRepository)
	teamsHandler := teams.NewHandler(teamsService)

	tasksRepository := tasks.NewRepository(db)
	tasksListCache := tasks.NewListCache(redisCache)
	tasksService := tasks.NewService(tasksRepository, teamsService, tasksListCache)
	tasksHandler := tasks.NewHandler(tasksService)

	commentsRepository := comments.NewRepository(db)
	commentsService := comments.NewService(commentsRepository, tasksService, teamsService)
	commentsHandler := comments.NewHandler(commentsService)

	analyticsRepository := analytics.NewRepository(db)
	analyticsService := analytics.NewService(analyticsRepository, teamsService)
	analyticsHandler := analytics.NewHandler(analyticsService)

	handler := http.NewServeMux()
	authHandler.RegisterRoutes(handler)
	teamsHandler.RegisterRoutes(handler, authMiddleware.Authenticate)
	tasksHandler.RegisterRoutes(handler, authMiddleware.Authenticate)
	commentsHandler.RegisterRoutes(handler, authMiddleware.Authenticate)
	analyticsHandler.RegisterRoutes(handler, authMiddleware.Authenticate)

	return &App{
		config: cfg,
		db:     db,
		cache:  redisCache,
		server: httpserver.New(cfg.HTTP, handler),
	}, nil
}

// Run запускает приложение и останавливает его при отмене контекста
func (a *App) Run(ctx context.Context) error {
	appContext, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	var serverGroup sync.WaitGroup
	serverGroup.Add(1)

	go func() {
		defer serverGroup.Done()

		slog.Info("starting HTTP server", "address", a.config.HTTP.Address())
		cancel(a.server.Run())
	}()

	<-appContext.Done()

	if ctx.Err() == nil {
		serverGroup.Wait()
		if err := context.Cause(appContext); err != context.Canceled {
			return err
		}

		return nil
	}
	slog.Info("shutting down HTTP server")

	shutdownContext, shutdownCancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		a.config.App.ShutdownTimeout,
	)
	defer shutdownCancel()

	if err := a.server.Shutdown(shutdownContext); err != nil {
		return err
	}
	serverGroup.Wait()
	slog.Info("HTTP server stopped")

	return nil
}

func (a *App) Close() error {
	var closeErrors []error
	if err := a.cache.Close(); err != nil {
		closeErrors = append(closeErrors, err)
	}
	if err := a.db.Close(); err != nil {
		closeErrors = append(closeErrors, fmt.Errorf("database: close connection pool: %w", err))
	}

	return errors.Join(closeErrors...)
}
