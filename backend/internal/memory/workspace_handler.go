package memory

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/rube11/rev-eyes/backend/internal/auth"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

var (
	ErrWorkspaceEditorRequired   = errors.New("workspace memory editor is required")
	ErrWorkspaceVerifierRequired = errors.New("workspace token verifier is required")
)

// WorkspaceEditor applies a user's edit to one of their memories.
type WorkspaceEditor interface {
	EditByID(context.Context, tool.Scope, string, WorkspaceEdit) error
}

// WorkspaceHandler serves PATCH /workspace/memories/{memory_id} for the
// signed-in web and glasses workspace.
type WorkspaceHandler struct {
	verifier         auth.TokenVerifier
	editor           WorkspaceEditor
	workspaceChanged func(userID string)
}

func NewWorkspaceHandler(
	verifier auth.TokenVerifier,
	editor WorkspaceEditor,
	workspaceChanged func(userID string),
) (*WorkspaceHandler, error) {
	if verifier == nil {
		return nil, ErrWorkspaceVerifierRequired
	}
	if editor == nil {
		return nil, ErrWorkspaceEditorRequired
	}
	return &WorkspaceHandler{
		verifier:         verifier,
		editor:           editor,
		workspaceChanged: workspaceChanged,
	}, nil
}

func (h *WorkspaceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPatch {
		w.Header().Set("Allow", "PATCH")
		writeWorkspaceError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
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

	var input struct {
		Action  WorkspaceEditAction `json:"action"`
		Title   string              `json:"title"`
		Summary string              `json:"summary"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeWorkspaceError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeWorkspaceError(w, http.StatusBadRequest, "invalid_request")
		return
	}

	memoryID := strings.TrimSpace(r.PathValue("memory_id"))
	err = h.editor.EditByID(r.Context(), tool.Scope{UserID: userID}, memoryID, WorkspaceEdit{
		Action:  input.Action,
		Title:   input.Title,
		Summary: input.Summary,
	})
	switch {
	case err == nil:
	case errors.Is(err, ErrInvalidMemoryEdit):
		writeWorkspaceError(w, http.StatusBadRequest, "invalid_request")
		return
	case errors.Is(err, ErrMemoryNotFound):
		writeWorkspaceError(w, http.StatusNotFound, "not_found")
		return
	default:
		slog.ErrorContext(r.Context(), "workspace memory edit failed",
			slog.String("memory_id", memoryID), slog.String("error", err.Error()))
		writeWorkspaceError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if h.workspaceChanged != nil {
		h.workspaceChanged(userID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeWorkspaceError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{Error: code})
}
