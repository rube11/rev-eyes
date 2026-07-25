package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func TestTicketStoreCreatesOneTimeScopedTickets(t *testing.T) {
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	store := NewTicketStore()
	store.now = func() time.Time { return now }

	issuedScope := tool.Scope{
		UserID:    " user-123 ",
		SessionID: " session-123 ",
		TimeZone:  " America/Los_Angeles ",
	}
	ticket, expiresAt, err := store.Issue(issuedScope)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if expiresAt != now.Add(time.Minute) {
		t.Fatalf("expiresAt = %v", expiresAt)
	}

	scope, err := store.Consume(ticket)
	if err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	if scope.UserID != "user-123" ||
		scope.SessionID != "session-123" ||
		scope.TimeZone != "America/Los_Angeles" {
		t.Fatalf("scope = %+v", scope)
	}
	if _, err := store.Consume(ticket); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("second Consume() error = %v, want %v", err, ErrInvalidTicket)
	}

	secondTicket, _, err := store.Issue(tool.Scope{
		UserID:    "user-123",
		SessionID: "session-123",
		TimeZone:  "America/Los_Angeles",
	})
	if err != nil {
		t.Fatalf("second Issue() error = %v", err)
	}
	secondScope, err := store.Consume(secondTicket)
	if err != nil {
		t.Fatalf("second Consume() error = %v", err)
	}
	if secondScope != scope {
		t.Fatalf("second scope = %+v, want %+v", secondScope, scope)
	}
}

func TestTicketStoreRejectsExpiredTicket(t *testing.T) {
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	store := NewTicketStore()
	store.now = func() time.Time { return now }

	ticket, _, err := store.Issue(tool.Scope{
		UserID:    "user-123",
		SessionID: "session-123",
		TimeZone:  "UTC",
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	now = now.Add(time.Minute)

	if _, err := store.Consume(ticket); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("Consume() error = %v, want %v", err, ErrInvalidTicket)
	}
}

func TestTicketHandlerExchangesBearerTokenForTicket(t *testing.T) {
	store := NewTicketStore()
	verifier := func(_ context.Context, token string) (string, error) {
		if token != "access-token" {
			return "", ErrInvalidAccessToken
		}
		return "user-123", nil
	}
	resolveSession := func(_ context.Context, userID string) (string, error) {
		if userID != "user-123" {
			t.Fatalf("userID = %q", userID)
		}
		return "session-123", nil
	}
	handler, err := NewTicketHandler(verifier, resolveSession, store)
	if err != nil {
		t.Fatalf("NewTicketHandler() error = %v", err)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/auth/ws-ticket",
		strings.NewReader(`{"time_zone":"America/Los_Angeles"}`),
	)
	request.Header.Set("Authorization", "Bearer access-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusCreated)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}

	var body struct {
		Ticket    string    `json:"ticket"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if body.Ticket == "" || body.ExpiresAt.IsZero() {
		t.Fatalf("response body = %+v", body)
	}
	scope, err := store.Consume(body.Ticket)
	if err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	if scope.UserID != "user-123" ||
		scope.SessionID != "session-123" ||
		scope.TimeZone != "America/Los_Angeles" {
		t.Fatalf("scope = %+v", scope)
	}
}

func TestTicketStoreRejectsInvalidTimeZone(t *testing.T) {
	t.Parallel()

	store := NewTicketStore()
	for _, test := range []struct {
		timeZone string
		wantErr  error
	}{
		{timeZone: "", wantErr: ErrTimeZoneRequired},
		{timeZone: "not/a-time-zone", wantErr: ErrTimeZoneInvalid},
	} {
		_, _, err := store.Issue(tool.Scope{
			UserID:    "user-123",
			SessionID: "session-123",
			TimeZone:  test.timeZone,
		})
		if !errors.Is(err, test.wantErr) {
			t.Fatalf("Issue(time zone %q) error = %v", test.timeZone, err)
		}
	}
}

func TestTicketHandlerRejectsMissingBearerToken(t *testing.T) {
	store := NewTicketStore()
	handler, err := NewTicketHandler(
		func(context.Context, string) (string, error) {
			t.Fatal("Verify() was called")
			return "", nil
		},
		func(context.Context, string) (string, error) {
			t.Fatal("resolveSession() was called")
			return "", nil
		},
		store,
	)
	if err != nil {
		t.Fatalf("NewTicketHandler() error = %v", err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodPost, "/auth/ws-ticket", nil),
	)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestTicketHandlerHidesDisallowedEmail(t *testing.T) {
	t.Parallel()

	handler, err := NewTicketHandler(
		func(context.Context, string) (string, error) {
			return "", ErrEmailNotAllowed
		},
		func(context.Context, string) (string, error) {
			t.Fatal("resolveSession() was called")
			return "", nil
		},
		NewTicketStore(),
	)
	if err != nil {
		t.Fatalf("NewTicketHandler() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/auth/ws-ticket", nil)
	request.Header.Set("Authorization", "Bearer valid-but-disallowed-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if !strings.Contains(response.Body.String(), "unauthorized") {
		t.Fatalf("body = %q", response.Body.String())
	}
}
