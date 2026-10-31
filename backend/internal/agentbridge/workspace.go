package agentbridge

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/rube11/rev-eyes/backend/internal/auth"
)

// WorkspaceHandler exposes the same owner-bound read gateway used by tools.
// Explicit control actions are authenticated separately from model tools.
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
	if r.Method != http.MethodGet && (r.Method != http.MethodPost || r.PathValue("action") == "") {
		w.Header().Set("Allow", "GET, POST")
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
	if r.Method == http.MethodPost {
		h.control(w, r, owner)
		return
	}
	var connections []Envelope
	if id := r.URL.Query().Get("connection_id"); id != "" {
		cursor, parseErr := strconv.Atoi(r.URL.Query().Get("cursor"))
		if parseErr != nil {
			http.Error(w, "invalid cursor", http.StatusBadRequest)
			return
		}
		connections, err = h.gateway.Page(r.Context(), owner, id, cursor)
	} else {
		connections, err = h.gateway.List(r.Context(), owner)
	}
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

func (h *WorkspaceHandler) control(w http.ResponseWriter, r *http.Request, owner string) {
	var input struct {
		ConnectionID string `json:"connection_id"`
		ThreadID     string `json:"thread_id"`
		Text         string `json:"text"`
		RequestID    string `json:"request_id"`
		After        *int   `json:"after_position"`
		FromStart    bool   `json:"from_start"`
	}
	raw, err := readControlBody(w, r)
	if err != nil {
		controlError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	action := r.PathValue("action")
	if action == "messages" {
		err = decodeBounded(raw, &input, 20000, "connection_id", "thread_id", "text", "request_id")
	} else if action == "thread" {
		fields := []string{"connection_id", "thread_id"}
		var keys map[string]json.RawMessage
		if json.Unmarshal(raw, &keys) == nil {
			if _, ok := keys["after_position"]; ok {
				fields = append(fields, "after_position")
			}
			if _, ok := keys["from_start"]; ok {
				fields = append(fields, "from_start")
			}
		}
		err = decodeBounded(raw, &input, 20000, fields...)
		if input.After != nil && (*input.After < 0 || input.FromStart) {
			err = errors.New("invalid position")
		}
	} else {
		controlError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		controlError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var data json.RawMessage
	if action == "messages" {
		data, err = h.gateway.Send(r.Context(), owner, input.ConnectionID, input.ThreadID, input.Text, input.RequestID)
	} else {
		after := -1
		if input.After != nil {
			after = *input.After
		}
		if input.After == nil && !input.FromStart {
			data, err = h.gateway.Open(r.Context(), owner, input.ConnectionID, input.ThreadID)
		} else {
			data, err = h.gateway.OpenPage(r.Context(), owner, input.ConnectionID, input.ThreadID, after)
		}
	}
	if errors.Is(err, ErrDeliveryUnknown) {
		controlError(w, http.StatusBadGateway, "delivery_unknown")
		return
	}
	if err != nil {
		controlError(w, http.StatusServiceUnavailable, "agent_unavailable")
		return
	}
	_, _ = w.Write(data)
}

func agentUnauthorized(w http.ResponseWriter) {
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
}
