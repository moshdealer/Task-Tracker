package teams

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"task-tracker/internal/auth"
)

type teamService interface {
	Create(ctx context.Context, userID uint64, input CreateInput) (Team, error)
	List(ctx context.Context, userID uint64) ([]Team, error)
	Invite(ctx context.Context, actorID, teamID uint64, input InviteInput) error
	ChangeMemberRole(
		ctx context.Context,
		actorID, teamID, memberID uint64,
		input ChangeMemberRoleInput,
	) error
}

// Handler обрабатывает HTTP-запросы модуля команд
type Handler struct {
	service teamService
}

// NewHandler создаёт HTTP-обработчик команд
func NewHandler(service teamService) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes регистрирует защищённые маршруты команд
func (h *Handler) RegisterRoutes(mux *http.ServeMux, authenticate func(http.Handler) http.Handler) {
	mux.Handle("POST /api/v1/teams", authenticate(http.HandlerFunc(h.create)))
	mux.Handle("GET /api/v1/teams", authenticate(http.HandlerFunc(h.list)))
	mux.Handle("POST /api/v1/teams/{id}/invite", authenticate(http.HandlerFunc(h.invite)))
	mux.Handle(
		"PUT /api/v1/teams/{id}/members/{user_id}/role",
		authenticate(http.HandlerFunc(h.changeMemberRole)),
	)
}

type createTeamRequest struct {
	Name string `json:"name"`
}

type teamResponse struct {
	ID        uint64    `json:"id"`
	Name      string    `json:"name"`
	CreatedBy uint64    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

type inviteMemberRequest struct {
	Email string `json:"email"`
	Role  Role   `json:"role"`
}

type changeMemberRoleRequest struct {
	Role Role `json:"role"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func (h *Handler) create(writer http.ResponseWriter, request *http.Request) {
	userID, ok := currentUserID(writer, request)
	if !ok {
		return
	}

	var body createTeamRequest
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid request")
		return
	}

	team, err := h.service.Create(request.Context(), userID, CreateInput{Name: body.Name})
	if err != nil {
		if errors.Is(err, ErrInvalidTeamName) {
			writeError(writer, http.StatusBadRequest, "invalid team name")
			return
		}

		slog.Error("create team", "error", err, "user_id", userID)
		writeError(writer, http.StatusInternalServerError, "internal server error")
		return
	}

	writeJSON(writer, http.StatusCreated, newTeamResponse(team))
}

func (h *Handler) list(writer http.ResponseWriter, request *http.Request) {
	userID, ok := currentUserID(writer, request)
	if !ok {
		return
	}

	userTeams, err := h.service.List(request.Context(), userID)
	if err != nil {
		slog.Error("list teams", "error", err, "user_id", userID)
		writeError(writer, http.StatusInternalServerError, "internal server error")
		return
	}

	response := make([]teamResponse, 0, len(userTeams))
	for _, team := range userTeams {
		response = append(response, newTeamResponse(team))
	}
	writeJSON(writer, http.StatusOK, response)
}

func (h *Handler) invite(writer http.ResponseWriter, request *http.Request) {
	userID, ok := currentUserID(writer, request)
	if !ok {
		return
	}

	teamID, err := strconv.ParseUint(request.PathValue("id"), 10, 64)
	if err != nil || teamID == 0 {
		writeError(writer, http.StatusBadRequest, "invalid team id")
		return
	}

	var body inviteMemberRequest
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid request")
		return
	}

	err = h.service.Invite(request.Context(), userID, teamID, InviteInput{
		Email: body.Email,
		Role:  body.Role,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidInviteEmail), errors.Is(err, ErrInvalidInviteRole):
			writeError(writer, http.StatusBadRequest, "invalid invitation data")
		case errors.Is(err, ErrAccessDenied):
			writeError(writer, http.StatusForbidden, "access denied")
		case errors.Is(err, ErrInviteeNotFound):
			writeError(writer, http.StatusNotFound, "user not found")
		case errors.Is(err, ErrAlreadyMember):
			writeError(writer, http.StatusConflict, "user is already a team member")
		default:
			slog.Error("invite team member", "error", err, "user_id", userID, "team_id", teamID)
			writeError(writer, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	writer.WriteHeader(http.StatusNoContent)
}

func (h *Handler) changeMemberRole(writer http.ResponseWriter, request *http.Request) {
	actorID, ok := currentUserID(writer, request)
	if !ok {
		return
	}

	teamID, err := strconv.ParseUint(request.PathValue("id"), 10, 64)
	if err != nil || teamID == 0 {
		writeError(writer, http.StatusBadRequest, "invalid team id")
		return
	}
	memberID, err := strconv.ParseUint(request.PathValue("user_id"), 10, 64)
	if err != nil || memberID == 0 {
		writeError(writer, http.StatusBadRequest, "invalid member id")
		return
	}

	var body changeMemberRoleRequest
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid request")
		return
	}

	err = h.service.ChangeMemberRole(
		request.Context(),
		actorID,
		teamID,
		memberID,
		ChangeMemberRoleInput{Role: body.Role},
	)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidTeamID),
			errors.Is(err, ErrInvalidMemberID),
			errors.Is(err, ErrInvalidMemberRole):
			writeError(writer, http.StatusBadRequest, "invalid role change data")
		case errors.Is(err, ErrAccessDenied), errors.Is(err, ErrOwnerRoleImmutable):
			writeError(writer, http.StatusForbidden, "access denied")
		case errors.Is(err, ErrMembershipNotFound):
			writeError(writer, http.StatusNotFound, "team member not found")
		default:
			slog.Error(
				"change team member role",
				"error", err,
				"user_id", actorID,
				"team_id", teamID,
				"member_id", memberID,
			)
			writeError(writer, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	writer.WriteHeader(http.StatusNoContent)
}

func currentUserID(writer http.ResponseWriter, request *http.Request) (uint64, bool) {
	userID, ok := auth.UserIDFromContext(request.Context())
	if !ok {
		writeError(writer, http.StatusUnauthorized, "unauthorized")
		return 0, false
	}

	return userID, true
}

func newTeamResponse(team Team) teamResponse {
	return teamResponse{
		ID:        team.ID,
		Name:      team.Name,
		CreatedBy: team.CreatedBy,
		CreatedAt: team.CreatedAt,
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
