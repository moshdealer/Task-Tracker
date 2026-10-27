package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"task-tracker/internal/auth"
	"task-tracker/internal/platform/config"
	"task-tracker/internal/platform/database"
	"task-tracker/internal/platform/httpserver"
	"task-tracker/internal/platform/jwt"
	"task-tracker/internal/platform/logger"
	"task-tracker/migrations"
)

// App объединяет зависимости приложения и управляет их жизненным циклом
type App struct {
	config config.Config
	db     *sql.DB
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

	tokenManager := jwt.New(cfg.JWT)
	authRepository := auth.NewRepository(db)
	authService := auth.NewService(authRepository, tokenManager)
	authHandler := auth.NewHandler(authService)

	handler := http.NewServeMux()
	authHandler.RegisterRoutes(handler)

	return &App{
		config: cfg,
		db:     db,
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

// Close освобождает ресурсы приложения
func (a *App) Close() error {
	if err := a.db.Close(); err != nil {
		return fmt.Errorf("database: close connection pool: %w", err)
	}

	return nil
}
