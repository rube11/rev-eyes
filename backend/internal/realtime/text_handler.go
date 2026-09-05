package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rube11/rev-eyes/backend/internal/auth"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

// TextHandler shares the audio turn coordinator and agent, but replies only to
// the requesting browser. The authenticated owner must own the selected session.
func (s *Server) TextHandler(verify auth.TokenVerifier, reopen func(context.Context, tool.Scope) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		fail := func(status int, code string) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
		}
		if r.Method != http.MethodPost {
			fail(http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		token, ok := auth.BearerToken(r.Header.Get("Authorization"))
		if !ok {
			fail(http.StatusUnauthorized, "unauthorized")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		userID, err := verify(ctx, token)
		if err != nil {
			fail(http.StatusUnauthorized, "unauthorized")
			return
		}
		var input struct {
			Text     string `json:"text"`
			TimeZone string `json:"time_zone"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			fail(http.StatusBadRequest, "invalid_request")
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			fail(http.StatusBadRequest, "invalid_request")
			return
		}
		input.Text = strings.TrimSpace(input.Text)
		var id pgtype.UUID
		sessionID := r.PathValue("session_id")
		if id.Scan(sessionID) != nil || !id.Valid || input.Text == "" || utf8.RuneCountInString(input.Text) > 4000 {
			fail(http.StatusBadRequest, "invalid_request")
			return
		}
		if input.TimeZone == "" {
			input.TimeZone = "UTC"
		}
		if _, err := time.LoadLocation(input.TimeZone); err != nil {
			fail(http.StatusBadRequest, "invalid_request")
			return
		}
		scope := tool.Scope{UserID: userID, SessionID: sessionID, TimeZone: input.TimeZone, AlwaysRespond: true}
		release, err := s.turns.acquire(ctx, scope)
		if err != nil {
			fail(http.StatusRequestTimeout, "turn_timeout")
			return
		}
		defer release()
		if err := reopen(ctx, scope); err != nil {
			if errors.Is(err, session.ErrSessionUnavailable) {
				fail(http.StatusNotFound, "not_found")
			} else {
				fail(http.StatusInternalServerError, "unavailable")
			}
			return
		}
		result, err := s.handlers.Utterance(ctx, scope, input.Text)
		s.hub.WorkspaceChanged(userID, append(result.WorkspaceResources, WorkspaceConversations)...)
		if err != nil {
			fail(http.StatusBadGateway, "turn_failed_check_log")
			return
		}
		_ = json.NewEncoder(w).Encode(struct {
			Text string `json:"text"`
		}{Text: result.Text})
	})
}
