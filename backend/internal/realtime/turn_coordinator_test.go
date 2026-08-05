package realtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func TestTurnCoordinatorSerializesOneSession(t *testing.T) {
	coordinator := newTurnCoordinator()
	scope := tool.Scope{UserID: "user", SessionID: "session"}
	firstRelease, err := coordinator.acquire(context.Background(), scope)
	if err != nil {
		t.Fatalf("first acquire error = %v", err)
	}

	secondAcquired := make(chan func(), 1)
	go func() {
		release, acquireErr := coordinator.acquire(context.Background(), scope)
		if acquireErr == nil {
			secondAcquired <- release
		}
	}()

	select {
	case release := <-secondAcquired:
		release()
		t.Fatal("second turn acquired the same session before the first released it")
	case <-time.After(20 * time.Millisecond):
	}

	firstRelease()
	secondRelease := receive(t, secondAcquired)
	secondRelease()
	if len(coordinator.gates) != 0 {
		t.Fatalf("retained gates = %d, want 0", len(coordinator.gates))
	}
}

func TestTurnCoordinatorAllowsDifferentSessions(t *testing.T) {
	coordinator := newTurnCoordinator()
	firstRelease, err := coordinator.acquire(
		context.Background(),
		tool.Scope{UserID: "user", SessionID: "session-a"},
	)
	if err != nil {
		t.Fatalf("first acquire error = %v", err)
	}
	defer firstRelease()

	secondRelease, err := coordinator.acquire(
		context.Background(),
		tool.Scope{UserID: "user", SessionID: "session-b"},
	)
	if err != nil {
		t.Fatalf("second acquire error = %v", err)
	}
	secondRelease()
}

func TestTurnCoordinatorCancelsWaitAndReleasesItsReference(t *testing.T) {
	coordinator := newTurnCoordinator()
	scope := tool.Scope{UserID: "user", SessionID: "session"}
	firstRelease, err := coordinator.acquire(context.Background(), scope)
	if err != nil {
		t.Fatalf("first acquire error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := coordinator.acquire(ctx, scope); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquire error = %v, want context.Canceled", err)
	}
	firstRelease()
	if len(coordinator.gates) != 0 {
		t.Fatalf("retained gates = %d, want 0", len(coordinator.gates))
	}
}
