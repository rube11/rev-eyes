package location

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

var (
	ErrUserIDRequired    = errors.New("user ID is required")
	ErrSessionIDRequired = errors.New("session ID is required")
	ErrInvalidLatitude   = errors.New("latitude must be between -90 and 90")
	ErrInvalidLongitude  = errors.New("longitude must be between -180 and 180")
	ErrInvalidAccuracy   = errors.New("accuracy must not be negative")
)

// Position is the latest location reported by a user's device.
type Position struct {
	Latitude       float64   `json:"latitude"`
	Longitude      float64   `json:"longitude"`
	AccuracyMeters float64   `json:"accuracy_meters,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Reader provides the latest known location within an authenticated session.
type Reader interface {
	Current(ctx context.Context, scope tool.Scope) (Position, bool, error)
}

type scopedPosition struct {
	userID   string
	position Position
}

// Store keeps the latest reported location for each authenticated application
// session in memory.
type Store struct {
	mu        sync.RWMutex
	positions map[string]scopedPosition
}

func NewStore() *Store {
	return &Store{positions: make(map[string]scopedPosition)}
}

// Update replaces the latest position for a trusted user and application session.
func (s *Store) Update(scope tool.Scope, position Position) error {
	scope, err := validateScope(scope)
	if err != nil {
		return err
	}
	if position.Latitude < -90 || position.Latitude > 90 {
		return ErrInvalidLatitude
	}
	if position.Longitude < -180 || position.Longitude > 180 {
		return ErrInvalidLongitude
	}
	if position.AccuracyMeters < 0 {
		return ErrInvalidAccuracy
	}
	if position.UpdatedAt.IsZero() {
		position.UpdatedAt = time.Now().UTC()
	} else {
		position.UpdatedAt = position.UpdatedAt.UTC()
	}

	s.mu.Lock()
	if s.positions == nil {
		s.positions = make(map[string]scopedPosition)
	}
	s.positions[scope.SessionID] = scopedPosition{
		userID:   scope.UserID,
		position: position,
	}
	s.mu.Unlock()

	return nil
}

// Current returns the latest position only when both the trusted user and
// application session match the stored owner.
func (s *Store) Current(ctx context.Context, scope tool.Scope) (Position, bool, error) {
	if err := ctx.Err(); err != nil {
		return Position{}, false, err
	}

	scope, err := validateScope(scope)
	if err != nil {
		return Position{}, false, err
	}

	s.mu.RLock()
	stored, found := s.positions[scope.SessionID]
	s.mu.RUnlock()
	if !found || stored.userID != scope.UserID {
		return Position{}, false, nil
	}

	return stored.position, true, nil
}

// Delete removes a location only when the trusted user owns the session entry.
func (s *Store) Delete(scope tool.Scope) {
	scope, err := validateScope(scope)
	if err != nil {
		return
	}

	s.mu.Lock()
	stored, found := s.positions[scope.SessionID]
	if found && stored.userID == scope.UserID {
		delete(s.positions, scope.SessionID)
	}
	s.mu.Unlock()
}

func validateScope(scope tool.Scope) (tool.Scope, error) {
	scope.UserID = strings.TrimSpace(scope.UserID)
	if scope.UserID == "" {
		return tool.Scope{}, ErrUserIDRequired
	}
	scope.SessionID = strings.TrimSpace(scope.SessionID)
	if scope.SessionID == "" {
		return tool.Scope{}, ErrSessionIDRequired
	}
	return scope, nil
}
