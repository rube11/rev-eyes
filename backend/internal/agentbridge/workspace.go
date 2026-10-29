package agentbridge

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/rube11/rev-eyes/backend/internal/auth"
)

// WorkspaceHandler exposes the same owner-bound read gateway used by tools.
// Configuration and credentials remain server-side. There are no write routes.
type WorkspaceHandler struct {
	verifier auth.TokenVerifier
	gateway  *Gateway
}

func NewWorkspaceHandler(verifier auth.TokenVerifier, gateway *Gateway) (*WorkspaceHandler, error) {
	if verifier == nil || gateway == nil {
		return nil, errors.New("agent workspace requires verifier and gateway")
	}
	return &WorkspaceHandler{verifier: verifier, gateway: gateway}, nil
}

func (h *WorkspaceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "method_not_allowed"})
		return
	}
	token, ok := auth.BearerToken(r.Header.Get("Authorization"))
	if !ok {
		agentUnauthorized(w)
		return
	}
	owner, err := h.verifier(r.Context(), token)
	if err != nil || strings.TrimSpace(owner) == "" {
		agentUnauthorized(w)
		return
	}
	// Never accept an owner, host, token, target or cursor from query/body input.
	connections, err := h.gateway.List(r.Context(), owner)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "agent_status_unavailable"})
		return
	}
	_ = json.NewEncoder(w).Encode(struct {
		Connections []Envelope `json:"connections"`
		Limit       int        `json:"limit"`
	}{Connections: connections, Limit: 20})
}

func agentUnauthorized(w http.ResponseWriter) {
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
}
