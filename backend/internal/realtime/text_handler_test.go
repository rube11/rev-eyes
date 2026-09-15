package realtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const chatTestSession = "11111111-1111-4111-8111-111111111111"

func TestTextChatUsesTrustedOwnerAndSelectedSession(t *testing.T) {
	var resumed tool.Scope
	server := NewServer(nil, Handlers{Utterance: func(_ context.Context, scope tool.Scope, text string) (UtteranceResult, error) {
		if scope != resumed || scope.UserID != "trusted-owner" || scope.SessionID != chatTestSession || scope.TimeZone != "America/Los_Angeles" || text != "hello" {
			t.Fatalf("incorrect scope or text: %+v %q", scope, text)
		}
		if !scope.AlwaysRespond {
			t.Fatal("typed turn must require a reply")
		}
		return UtteranceResult{Text: "Hello back"}, nil
	}})
	handler := server.TextHandler(func(context.Context, string) (string, error) { return "trusted-owner", nil }, func(_ context.Context, scope tool.Scope) error { resumed = scope; return nil })
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"text":" hello ","time_zone":"America/Los_Angeles"}`))
	request.SetPathValue("session_id", chatTestSession)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Hello back") {
		t.Fatalf("response: %d %s", response.Code, response.Body.String())
	}
}

func TestTextChatRejectsInvalidOrUnauthorizedRequestsBeforeAgent(t *testing.T) {
	for _, tc := range []struct {
		name, body, token, id string
		unauthorized, missing bool
		status                int
	}{
		{name: "missing token", body: `{"text":"hello"}`, id: chatTestSession, status: 401},
		{name: "bad token", body: `{"text":"hello"}`, token: "test", id: chatTestSession, unauthorized: true, status: 401},
		{name: "another owners session", body: `{"text":"hello"}`, token: "test", id: chatTestSession, missing: true, status: 404},
		{name: "blank", body: `{"text":"  "}`, token: "test", id: chatTestSession, status: 400},
		{name: "invalid id", body: `{"text":"hello"}`, token: "test", id: "bad", status: 400},
		{name: "untrusted owner", body: `{"text":"hello","user_id":"other"}`, token: "test", id: chatTestSession, status: 400},
		{name: "trailing json", body: `{"text":"hello"}{}`, token: "test", id: chatTestSession, status: 400},
		{name: "too long", body: `{"text":"` + strings.Repeat("x", 4001) + `"}`, token: "test", id: chatTestSession, status: 400},
		{name: "bad timezone", body: `{"text":"hello","time_zone":"invalid/zone"}`, token: "test", id: chatTestSession, status: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := NewServer(nil, Handlers{Utterance: func(context.Context, tool.Scope, string) (UtteranceResult, error) {
				t.Fatal("agent must not run")
				return UtteranceResult{}, nil
			}})
			handler := server.TextHandler(func(context.Context, string) (string, error) {
				if tc.unauthorized {
					return "", errors.New("denied")
				}
				return "owner", nil
			}, func(context.Context, tool.Scope) error {
				if tc.missing {
					return session.ErrSessionUnavailable
				}
				return nil
			})
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			request.SetPathValue("session_id", tc.id)
			if tc.token != "" {
				request.Header.Set("Authorization", "Bearer "+tc.token)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("status %d, want %d", response.Code, tc.status)
			}
		})
	}
}

func TestTextChatDistinguishesIgnoredTurnsAndFailures(t *testing.T) {
	for _, fail := range []bool{false, true} {
		server := NewServer(nil, Handlers{Utterance: func(context.Context, tool.Scope, string) (UtteranceResult, error) {
			if fail {
				return UtteranceResult{}, errors.New("private upstream error")
			}
			return UtteranceResult{}, nil
		}})
		handler := server.TextHandler(func(context.Context, string) (string, error) { return "owner", nil }, func(context.Context, tool.Scope) error { return nil })
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"text":"hello"}`))
		request.SetPathValue("session_id", chatTestSession)
		request.Header.Set("Authorization", "Bearer test")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if fail {
			if response.Code != 502 || strings.Contains(response.Body.String(), "private") {
				t.Fatal("expected sanitized failure")
			}
		} else if response.Code != 200 || !strings.Contains(response.Body.String(), `"text":""`) {
			t.Fatal("expected explicit empty reply")
		}
	}
}
