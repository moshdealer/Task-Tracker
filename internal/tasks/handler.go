package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"task-tracker/internal/auth"
)

type taskService interface {
	Create(ctx context.Context, userID uint64, input CreateInput) (Task, error)
	List(ctx context.Context, userID uint64, filter ListFilter) ([]Task, error)
	Update(ctx context.Context, userID, taskID uint64, input UpdateInput) (Task, error)
	History(ctx context.Context, userID, taskID uint64) ([]HistoryEntry, error)
}

// Handler обрабатывает HTTP-запросы модуля задач
type Handler struct {
	service taskService
}

// NewHandler создаёт HTTP-обработчик задач
func NewHandler(service taskService) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes регистрирует защищённые маршруты задач
func (h *Handler) RegisterRoutes(mux *http.ServeMux, authenticate func(http.Handler) http.Handler) {
	mux.Handle("POST /api/v1/tasks", authenticate(http.HandlerFunc(h.create)))
	mux.Handle("GET /api/v1/tasks", authenticate(http.HandlerFunc(h.list)))
	mux.Handle("PUT /api/v1/tasks/{id}", authenticate(http.HandlerFunc(h.update)))
	mux.Handle("GET /api/v1/tasks/{id}/history", authenticate(http.HandlerFunc(h.history)))
}

type createTaskRequest struct {
	TeamID      uint64                `json:"team_id"`
	Title       string                `json:"title"`
	Description *string               `json:"description"`
	Status      optionalValue[Status] `json:"status"`
	AssigneeID  *uint64               `json:"assignee_id"`
}

type optionalValue[T any] struct {
	Set   bool
	Value *T
}

func (o *optionalValue[T]) UnmarshalJSON(data []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		o.Value = nil
		return nil
	}

	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	o.Value = &value
	return nil
}

type updateTaskRequest struct {
	Version     uint64                `json:"version"`
	Title       optionalValue[string] `json:"title"`
	Description optionalValue[string] `json:"description"`
	Status      optionalValue[Status] `json:"status"`
	AssigneeID  optionalValue[uint64] `json:"assignee_id"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func (h *Handler) create(writer http.ResponseWriter, request *http.Request) {
	userID, ok := currentUserID(writer, request)
	if !ok {
		return
	}

	var body createTaskRequest
	if err := decodeJSON(request, &body); err != nil || body.Status.Set && body.Status.Value == nil {
		writeError(writer, http.StatusBadRequest, "invalid request")
		return
	}

	status := StatusTodo
	if body.Status.Value != nil {
		status = *body.Status.Value
	}
	task, err := h.service.Create(request.Context(), userID, CreateInput{
		TeamID:      body.TeamID,
		Title:       body.Title,
		Description: body.Description,
		Status:      status,
		AssigneeID:  body.AssigneeID,
	})
	if err != nil {
		writeServiceError(writer, err, "create task", userID, 0)
		return
	}

	writeJSON(writer, http.StatusCreated, task)
}

func (h *Handler) list(writer http.ResponseWriter, request *http.Request) {
	userID, ok := currentUserID(writer, request)
	if !ok {
		return
	}

	filter, err := listFilterFromRequest(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid task filters")
		return
	}
	result, err := h.service.List(request.Context(), userID, filter)
	if err != nil {
		writeServiceError(writer, err, "list tasks", userID, 0)
		return
	}

	writeJSON(writer, http.StatusOK, result)
}

func (h *Handler) update(writer http.ResponseWriter, request *http.Request) {
	userID, ok := currentUserID(writer, request)
	if !ok {
		return
	}
	taskID, ok := taskIDFromRequest(writer, request)
	if !ok {
		return
	}

	var body updateTaskRequest
	if err := decodeJSON(request, &body); err != nil ||
		(body.Title.Set && body.Title.Value == nil) ||
		(body.Status.Set && body.Status.Value == nil) {
		writeError(writer, http.StatusBadRequest, "invalid request")
		return
	}

	input := UpdateInput{
		Version:        body.Version,
		TitleSet:       body.Title.Set,
		DescriptionSet: body.Description.Set,
		Description:    body.Description.Value,
		StatusSet:      body.Status.Set,
		AssigneeIDSet:  body.AssigneeID.Set,
		AssigneeID:     body.AssigneeID.Value,
	}
	if body.Title.Value != nil {
		input.Title = *body.Title.Value
	}
	if body.Status.Value != nil {
		input.Status = *body.Status.Value
	}

	task, err := h.service.Update(request.Context(), userID, taskID, input)
	if err != nil {
		writeServiceError(writer, err, "update task", userID, taskID)
		return
	}
	writeJSON(writer, http.StatusOK, task)
}

func (h *Handler) history(writer http.ResponseWriter, request *http.Request) {
	userID, ok := currentUserID(writer, request)
	if !ok {
		return
	}
	taskID, ok := taskIDFromRequest(writer, request)
	if !ok {
		return
	}

	history, err := h.service.History(request.Context(), userID, taskID)
	if err != nil {
		writeServiceError(writer, err, "list task history", userID, taskID)
		return
	}
	writeJSON(writer, http.StatusOK, history)
}

func listFilterFromRequest(request *http.Request) (ListFilter, error) {
	query := request.URL.Query()
	teamID, err := strconv.ParseUint(query.Get("team_id"), 10, 64)
	if err != nil || teamID == 0 {
		return ListFilter{}, ErrInvalidTeamID
	}

	filter := ListFilter{TeamID: teamID, Limit: 20}
	if query.Has("status") {
		status := Status(query.Get("status"))
		if !validStatus(status) {
			return ListFilter{}, ErrInvalidTaskStatus
		}
		filter.Status = &status
	}
	if query.Has("assignee_id") {
		assigneeID, err := strconv.ParseUint(query.Get("assignee_id"), 10, 64)
		if err != nil || assigneeID == 0 {
			return ListFilter{}, ErrInvalidAssignee
		}
		filter.AssigneeID = &assigneeID
	}
	if query.Has("limit") {
		limit, err := strconv.Atoi(query.Get("limit"))
		if err != nil || limit < 1 || limit > 100 {
			return ListFilter{}, ErrInvalidPagination
		}
		filter.Limit = limit
	}
	if query.Has("offset") {
		offset, err := strconv.Atoi(query.Get("offset"))
		if err != nil || offset < 0 {
			return ListFilter{}, ErrInvalidPagination
		}
		filter.Offset = offset
	}

	return filter, nil
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
	case errors.Is(err, ErrInvalidTeamID),
		errors.Is(err, ErrInvalidTaskID),
		errors.Is(err, ErrInvalidTaskTitle),
		errors.Is(err, ErrInvalidTaskStatus),
		errors.Is(err, ErrInvalidAssignee),
		errors.Is(err, ErrAssigneeNotMember),
		errors.Is(err, ErrInvalidPagination),
		errors.Is(err, ErrInvalidVersion):
		writeError(writer, http.StatusBadRequest, "invalid task data")
	case errors.Is(err, ErrAccessDenied):
		writeError(writer, http.StatusForbidden, "access denied")
	case errors.Is(err, ErrTaskNotFound):
		writeError(writer, http.StatusNotFound, "task not found")
	case errors.Is(err, ErrVersionConflict):
		writeError(writer, http.StatusConflict, "task version conflict")
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
