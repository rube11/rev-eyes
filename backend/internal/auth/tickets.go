package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const ticketTTL = time.Minute

var (
	ErrInvalidTicket  = errors.New("invalid or expired WebSocket ticket")
	ErrUserIDRequired = errors.New("user ID is required")
)

type ticketGrant struct {
	scope     tool.Scope
	expiresAt time.Time
}

// TicketStore holds short-lived, single-use WebSocket tickets in memory.
type TicketStore struct {
	mu      sync.Mutex
	tickets map[[sha256.Size]byte]ticketGrant
	now     func() time.Time
}

func NewTicketStore() *TicketStore {
	return &TicketStore{
		tickets: make(map[[sha256.Size]byte]ticketGrant),
		now:     time.Now,
	}
}

func (s *TicketStore) Issue(userID string) (string, time.Time, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "", time.Time{}, ErrUserIDRequired
	}

	ticket, err := randomID(32)
	if err != nil {
		return "", time.Time{}, err
	}
	sessionID, err := randomID(24)
	if err != nil {
		return "", time.Time{}, err
	}

	now := s.now().UTC()
	expiresAt := now.Add(ticketTTL)

	s.mu.Lock()
	for key, grant := range s.tickets {
		if !now.Before(grant.expiresAt) {
			delete(s.tickets, key)
		}
	}
	s.tickets[sha256.Sum256([]byte(ticket))] = ticketGrant{
		scope: tool.Scope{
			UserID:    userID,
			SessionID: sessionID,
		},
		expiresAt: expiresAt,
	}
	s.mu.Unlock()

	return ticket, expiresAt, nil
}

func (s *TicketStore) Consume(ticket string) (tool.Scope, error) {
	ticket = strings.TrimSpace(ticket)
	if ticket == "" {
		return tool.Scope{}, ErrInvalidTicket
	}

	key := sha256.Sum256([]byte(ticket))
	now := s.now().UTC()

	s.mu.Lock()
	grant, found := s.tickets[key]
	delete(s.tickets, key)
	s.mu.Unlock()

	if !found || !now.Before(grant.expiresAt) {
		return tool.Scope{}, ErrInvalidTicket
	}
	return grant.scope, nil
}

func randomID(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

type TicketHandler struct {
	verifier TokenVerifier
	tickets  *TicketStore
}

func NewTicketHandler(verifier TokenVerifier, tickets *TicketStore) (*TicketHandler, error) {
	if verifier == nil {
		return nil, errors.New("token verifier is required")
	}
	if tickets == nil {
		return nil, errors.New("ticket store is required")
	}
	return &TicketHandler{verifier: verifier, tickets: tickets}, nil
}

func (h *TicketHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	accessToken, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		unauthorized(w)
		return
	}
	userID, err := h.verifier(r.Context(), accessToken)
	if err != nil {
		unauthorized(w)
		return
	}

	ticket, expiresAt, err := h.tickets.Issue(userID)
	if err != nil {
		http.Error(w, "could not issue WebSocket ticket", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(struct {
		Ticket    string    `json:"ticket"`
		ExpiresAt time.Time `json:"expires_at"`
	}{
		Ticket:    ticket,
		ExpiresAt: expiresAt,
	})
}

func bearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	return parts[1], true
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}
