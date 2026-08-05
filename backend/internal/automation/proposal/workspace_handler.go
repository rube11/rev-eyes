package proposal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/rube11/rev-eyes/backend/internal/auth"
)

var (
	ErrWorkspaceCommanderRequired = errors.New("workspace automation commander is required")
	ErrWorkspaceVerifierRequired  = errors.New("workspace token verifier is required")
	ErrWorkspaceTriggerRequired   = errors.New("workspace schedule trigger is required")
)

type WorkspaceCommander interface {
	ResolveByID(context.Context, string, Kind, string, Status) (Resolution, error)
	DeleteByID(context.Context, string, Kind, string) (bool, error)
}

type WorkspaceHandler struct {
	verifier         auth.TokenVerifier
	commander        WorkspaceCommander
	triggerSchedule  func()
	workspaceChanged func(userID string, kind Kind)
}

func NewWorkspaceHandler(
	verifier auth.TokenVerifier,
	commander WorkspaceCommander,
	triggerSchedule func(),
) (*WorkspaceHandler, error) {
	if verifier == nil {
		return nil, ErrWorkspaceVerifierRequired
	}
	if commander == nil {
		return nil, ErrWorkspaceCommanderRequired
	}
	if triggerSchedule == nil {
		return nil, ErrWorkspaceTriggerRequired
	}
	return &WorkspaceHandler{
		verifier:        verifier,
		commander:       commander,
		triggerSchedule: triggerSchedule,
	}, nil
}

// SetWorkspaceChanged registers delivery for successful owner-scoped changes.
func (h *WorkspaceHandler) SetWorkspaceChanged(
	workspaceChanged func(userID string, kind Kind),
) {
	h.workspaceChanged = workspaceChanged
}

func (h *WorkspaceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	accessToken, ok := auth.BearerToken(r.Header.Get("Authorization"))
	if !ok {
		writeWorkspaceError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	userID, err := h.verifier(r.Context(), accessToken)
	if err != nil {
		writeWorkspaceError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	kind := Kind(strings.TrimSpace(r.PathValue("kind")))
	resourceID := strings.TrimSpace(r.PathValue("resource_id"))
	switch r.Method {
	case http.MethodPost:
		h.resolve(w, r, userID, kind, resourceID)
	case http.MethodDelete:
		h.delete(w, r, userID, kind, resourceID)
	default:
		w.Header().Set("Allow", "POST, DELETE")
		writeWorkspaceError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (h *WorkspaceHandler) resolve(
	w http.ResponseWriter,
	r *http.Request,
	userID string,
	kind Kind,
	resourceID string,
) {
	var input struct {
		Decision Status `json:"decision"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeWorkspaceError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeWorkspaceError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	resolution, err := h.commander.ResolveByID(
		r.Context(),
		userID,
		kind,
		resourceID,
		input.Decision,
	)
	if err != nil {
		h.handleCommandError(w, r, err)
		return
	}
	if resolution.Status == StatusAccepted {
		h.triggerSchedule()
	}
	if h.workspaceChanged != nil {
		h.workspaceChanged(userID, kind)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *WorkspaceHandler) delete(
	w http.ResponseWriter,
	r *http.Request,
	userID string,
	kind Kind,
	resourceID string,
) {
	cancellationQueued, err := h.commander.DeleteByID(
		r.Context(),
		userID,
		kind,
		resourceID,
	)
	if err != nil {
		h.handleCommandError(w, r, err)
		return
	}
	if cancellationQueued {
		h.triggerSchedule()
	}
	if h.workspaceChanged != nil {
		h.workspaceChanged(userID, kind)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *WorkspaceHandler) handleCommandError(
	w http.ResponseWriter,
	r *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, ErrAutomationNotFound):
		writeWorkspaceError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, ErrReminderTimePassed):
		writeWorkspaceError(w, http.StatusConflict, "reminder_time_passed")
	case errors.Is(err, ErrWatchExpired):
		writeWorkspaceError(w, http.StatusConflict, "watch_expired")
	case errors.Is(err, ErrWatchLimitReached):
		writeWorkspaceError(w, http.StatusConflict, "watch_limit_reached")
	case errors.Is(err, ErrKindInvalid),
		errors.Is(err, ErrResourceIDInvalid),
		errors.Is(err, ErrStatusInvalid),
		errors.Is(err, ErrUserIDInvalid):
		writeWorkspaceError(w, http.StatusBadRequest, "invalid_request")
	default:
		slog.ErrorContext(
			r.Context(),
			"workspace automation command failed",
			"error",
			err,
		)
		writeWorkspaceError(w, http.StatusInternalServerError, "internal_error")
	}
}

func writeWorkspaceError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{Error: code})
}
