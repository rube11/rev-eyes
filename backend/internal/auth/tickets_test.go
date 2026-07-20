package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTicketStoreCreatesOneTimeScopedTickets(t *testing.T) {
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	store := NewTicketStore()
	store.now = func() time.Time { return now }

	ticket, expiresAt, err := store.Issue(" user-123 ")
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
	if scope.UserID != "user-123" || scope.SessionID == "" {
		t.Fatalf("scope = %+v", scope)
	}
	if _, err := store.Consume(ticket); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("second Consume() error = %v, want %v", err, ErrInvalidTicket)
	}

	secondTicket, _, err := store.Issue("user-123")
	if err != nil {
		t.Fatalf("second Issue() error = %v", err)
	}
	secondScope, err := store.Consume(secondTicket)
	if err != nil {
		t.Fatalf("second Consume() error = %v", err)
	}
	if secondScope.SessionID == scope.SessionID {
		t.Fatal("separate tickets received the same session ID")
	}
}

func TestTicketStoreRejectsExpiredTicket(t *testing.T) {
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	store := NewTicketStore()
	store.now = func() time.Time { return now }

	ticket, _, err := store.Issue("user-123")
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
	handler, err := NewTicketHandler(verifier, store)
	if err != nil {
		t.Fatalf("NewTicketHandler() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/auth/ws-ticket", nil)
	request.Header.Set("Authorization", "Bearer access-token")
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
	if scope.UserID != "user-123" || scope.SessionID == "" {
		t.Fatalf("scope = %+v", scope)
	}
}

func TestTicketHandlerRejectsMissingBearerToken(t *testing.T) {
	store := NewTicketStore()
	handler, err := NewTicketHandler(
		func(context.Context, string) (string, error) {
			t.Fatal("Verify() was called")
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
