package location

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func TestRegisteredToolReturnsScopedLocation(t *testing.T) {
	t.Parallel()

	locations := NewStore()
	scope := tool.Scope{
		UserID:    "user-123",
		SessionID: "session-456",
	}
	if err := locations.Update(scope, Position{
		Latitude:       34.0522,
		Longitude:      -118.2437,
		AccuracyMeters: 8,
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	currentLocation, err := New(locations)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	registry := tool.NewRegistry()
	if err := registry.Register(currentLocation); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	executor, err := tool.NewExecutor(registry)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}

	result, err := executor.Execute(
		context.Background(),
		scope,
		"get_current_location",
		json.RawMessage(`{}`),
	)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var position Position
	if err := json.Unmarshal([]byte(result.Content), &position); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if position.Latitude != 34.0522 || position.Longitude != -118.2437 {
		t.Fatalf("Execute() coordinates = (%v, %v)", position.Latitude, position.Longitude)
	}
}

func TestDeleteRemovesLocation(t *testing.T) {
	t.Parallel()

	locations := NewStore()
	scope := tool.Scope{
		UserID:    "user-123",
		SessionID: "session-456",
	}
	if err := locations.Update(scope, Position{}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	locations.Delete(scope)
	_, found, err := locations.Current(context.Background(), scope)
	if err != nil {
		t.Fatalf("Current() error = %v", err)
	}
	if found {
		t.Fatal("Current() found = true after Delete()")
	}
}

func TestCurrentDoesNotExposeAnotherUsersSessionLocation(t *testing.T) {
	t.Parallel()

	locations := NewStore()
	owner := tool.Scope{
		UserID:    "user-123",
		SessionID: "session-456",
	}
	if err := locations.Update(owner, Position{Latitude: 34, Longitude: -118}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	requester := tool.Scope{
		UserID:    "user-789",
		SessionID: owner.SessionID,
	}
	_, found, err := locations.Current(context.Background(), requester)
	if err != nil {
		t.Fatalf("Current() error = %v", err)
	}
	if found {
		t.Fatal("Current() exposed a location owned by another user")
	}
}

func TestUpdateRequiresAuthenticatedUserAndSession(t *testing.T) {
	t.Parallel()

	locations := NewStore()
	if err := locations.Update(tool.Scope{SessionID: "session-456"}, Position{}); err != ErrUserIDRequired {
		t.Fatalf("Update() error = %v, want %v", err, ErrUserIDRequired)
	}
	if err := locations.Update(tool.Scope{UserID: "user-123"}, Position{}); err != ErrSessionIDRequired {
		t.Fatalf("Update() error = %v, want %v", err, ErrSessionIDRequired)
	}
}
