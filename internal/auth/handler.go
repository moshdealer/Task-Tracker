package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"
)

type authService interface {
	Register(ctx context.Context, input RegisterInput) (User, error)
	Login(ctx context.Context, input LoginInput) (string, error)
}

// Handler обрабатывает HTTP-запросы модуля аутентификации
type Handler struct {
	service authService
}

// NewHandler создаёт HTTP-обработчик аутентификации
func NewHandler(service authService) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes регистрирует маршруты аутентификации
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/register", h.register)
	mux.HandleFunc("POST /api/v1/login", h.login)
}

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

type registerResponse struct {
	ID        uint64    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token string `json:"token"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func (h *Handler) register(writer http.ResponseWriter, request *http.Request) {
	var body registerRequest
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid request")
		return
	}

	user, err := h.service.Register(request.Context(), RegisterInput{
		Email:    body.Email,
		Password: body.Password,
		Name:     body.Name,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidEmail),
			errors.Is(err, ErrInvalidPassword),
			errors.Is(err, ErrInvalidName):
			writeError(writer, http.StatusBadRequest, "invalid registration data")
		case errors.Is(err, ErrEmailAlreadyExists):
			writeError(writer, http.StatusConflict, "email already exists")
		default:
			slog.Error("register user", "error", err)
			writeError(writer, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	writeJSON(writer, http.StatusCreated, registerResponse{
		ID:        user.ID,
		Email:     user.Email,
		Name:      user.Name,
		CreatedAt: user.CreatedAt,
	})
}

func (h *Handler) login(writer http.ResponseWriter, request *http.Request) {
	var body loginRequest
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid request")
		return
	}

	token, err := h.service.Login(request.Context(), LoginInput{
		Email:    body.Email,
		Password: body.Password,
	})
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			writeError(writer, http.StatusUnauthorized, "invalid credentials")
			return
		}

		slog.Error("login user", "error", err)
		writeError(writer, http.StatusInternalServerError, "internal server error")
		return
	}

	writeJSON(writer, http.StatusOK, loginResponse{Token: token})
}

func decodeJSON(request *http.Request, destination any) error {
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain a single JSON object")
	}

	return nil
}

func writeJSON(writer http.ResponseWriter, status int, body any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)

	if err := json.NewEncoder(writer).Encode(body); err != nil {
		slog.Error("write JSON response", "error", err)
	}
}

func writeError(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, errorResponse{Error: message})
}
