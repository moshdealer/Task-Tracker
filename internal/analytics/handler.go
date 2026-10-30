package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"task-tracker/internal/auth"
)

type statsService interface {
	Stats(ctx context.Context, userID, teamID uint64) (Stats, error)
}

// Handler обрабатывает HTTP-запросы аналитики
type Handler struct {
	service statsService
}

// NewHandler создаёт HTTP-обработчик аналитики
func NewHandler(service statsService) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes регистрирует маршрут аналитики
func (h *Handler) RegisterRoutes(mux *http.ServeMux, authenticate func(http.Handler) http.Handler) {
	mux.Handle("GET /api/v1/teams/{team_id}/stats", authenticate(http.HandlerFunc(h.stats)))
}

type errorResponse struct {
	Error string `json:"error"`
}

func (h *Handler) stats(writer http.ResponseWriter, request *http.Request) {
	userID, ok := currentUserID(writer, request)
	if !ok {
		return
	}
	teamID, err := strconv.ParseUint(request.PathValue("team_id"), 10, 64)
	if err != nil || teamID == 0 {
		writeError(writer, http.StatusBadRequest, "invalid team id")
		return
	}

	report, err := h.service.Stats(request.Context(), userID, teamID)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidTeamID):
			writeError(writer, http.StatusBadRequest, "invalid team id")
		case errors.Is(err, ErrTeamNotFound):
			writeError(writer, http.StatusNotFound, "team not found")
		case errors.Is(err, ErrAccessDenied):
			writeError(writer, http.StatusForbidden, "access denied")
		default:
			slog.Error("get team stats", "error", err, "user_id", userID, "team_id", teamID)
			writeError(writer, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	writeJSON(writer, http.StatusOK, report)
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
