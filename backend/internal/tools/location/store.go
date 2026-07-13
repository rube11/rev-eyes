package location

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

var (
	ErrUserIDRequired   = errors.New("user ID is required")
	ErrInvalidLatitude  = errors.New("latitude must be between -90 and 90")
	ErrInvalidLongitude = errors.New("longitude must be between -180 and 180")
	ErrInvalidAccuracy  = errors.New("accuracy must not be negative")
)

// Position is the latest location reported by a user's device.
type Position struct {
	Latitude       float64   `json:"latitude"`
	Longitude      float64   `json:"longitude"`
	AccuracyMeters float64   `json:"accuracy_meters,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Reader provides the latest known location for a user.
type Reader interface {
	Current(ctx context.Context, userID string) (Position, bool, error)
}

// Store keeps the latest reported location for each user in memory.
type Store struct {
	mu        sync.RWMutex
	positions map[string]Position
}

func NewStore() *Store {
	return &Store{positions: make(map[string]Position)}
}

// Update replaces a user's latest known position.
func (s *Store) Update(userID string, position Position) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ErrUserIDRequired
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
		s.positions = make(map[string]Position)
	}
	s.positions[userID] = position
	s.mu.Unlock()

	return nil
}

// Current returns a user's latest known position.
func (s *Store) Current(ctx context.Context, userID string) (Position, bool, error) {
	if err := ctx.Err(); err != nil {
		return Position{}, false, err
	}

	userID = strings.TrimSpace(userID)
	if userID == "" {
		return Position{}, false, ErrUserIDRequired
	}

	s.mu.RLock()
	position, found := s.positions[userID]
	s.mu.RUnlock()

	return position, found, nil
}
