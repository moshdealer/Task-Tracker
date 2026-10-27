package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"task-tracker/internal/platform/config"
)

// Server представляет HTTP-сервер приложения
type Server struct {
	server *http.Server
}

// New создаёт HTTP-сервер с заданными адресом и тайм-аутами
func New(cfg config.HTTP, handler http.Handler) *Server {
	return &Server{
		server: &http.Server{
			Addr:              cfg.Address(),
			Handler:           handler,
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
			WriteTimeout:      cfg.WriteTimeout,
			IdleTimeout:       cfg.IdleTimeout,
		},
	}
}

// Run запускает приём запросов и блокируется до остановки сервера или ошибки
func (s *Server) Run() error {
	if err := s.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: listen and serve: %w", err)
	}

	return nil
}

// Shutdown корректно останавливает сервер, не прерывая активные запросы
func (s *Server) Shutdown(ctx context.Context) error {
	if err := s.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("http server: shutdown: %w", err)
	}

	return nil
}
