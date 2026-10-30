package comments

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"task-tracker/internal/auth"
)

type commentService interface {
	Create(ctx context.Context, userID, taskID uint64, content string) (Comment, error)
	List(ctx context.Context, userID, taskID uint64) ([]Comment, error)
}

// Handler обрабатывает HTTP-запросы комментариев
type Handler struct {
	service commentService
}

// NewHandler создаёт HTTP-обработчик комментариев
func NewHandler(service commentService) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes регистрирует защищённые маршруты комментариев
func (h *Handler) RegisterRoutes(mux *http.ServeMux, authenticate func(http.Handler) http.Handler) {
	mux.Handle("POST /api/v1/tasks/{id}/comments", authenticate(http.HandlerFunc(h.create)))
	mux.Handle("GET /api/v1/tasks/{id}/comments", authenticate(http.HandlerFunc(h.list)))
}

type createCommentRequest struct {
	Content string `json:"content"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func (h *Handler) create(writer http.ResponseWriter, request *http.Request) {
	userID, ok := currentUserID(writer, request)
	if !ok {
		return
	}
	taskID, ok := taskIDFromRequest(writer, request)
	if !ok {
		return
	}

	var body createCommentRequest
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid request")
		return
	}

	comment, err := h.service.Create(request.Context(), userID, taskID, body.Content)
	if err != nil {
		writeServiceError(writer, err, "create comment", userID, taskID)
		return
	}

	writeJSON(writer, http.StatusCreated, comment)
}

func (h *Handler) list(writer http.ResponseWriter, request *http.Request) {
	userID, ok := currentUserID(writer, request)
	if !ok {
		return
	}
	taskID, ok := taskIDFromRequest(writer, request)
	if !ok {
		return
	}

	result, err := h.service.List(request.Context(), userID, taskID)
	if err != nil {
		writeServiceError(writer, err, "list comments", userID, taskID)
		return
	}

	writeJSON(writer, http.StatusOK, result)
}

func taskIDFromRequest(writer http.ResponseWriter, request *http.Request) (uint64, bool) {
	taskID, err := strconv.ParseUint(request.PathValue("id"), 10, 64)
	if err != nil || taskID == 0 {
		writeError(writer, http.StatusBadRequest, "invalid task id")
		return 0, false
	}
	return taskID, true
}

func currentUserID(writer http.ResponseWriter, request *http.Request) (uint64, bool) {
	userID, ok := auth.UserIDFromContext(request.Context())
	if !ok {
		writer.Header().Set("WWW-Authenticate", "Bearer")
		writeError(writer, http.StatusUnauthorized, "unauthorized")
		return 0, false
	}
	return userID, true
}

func writeServiceError(writer http.ResponseWriter, err error, operation string, userID, taskID uint64) {
	switch {
	case errors.Is(err, ErrInvalidTaskID), errors.Is(err, ErrInvalidContent):
		writeError(writer, http.StatusBadRequest, "invalid comment data")
	case errors.Is(err, ErrTaskNotFound):
		writeError(writer, http.StatusNotFound, "task not found")
	default:
		slog.Error(operation, "error", err, "user_id", userID, "task_id", taskID)
		writeError(writer, http.StatusInternalServerError, "internal server error")
	}
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
