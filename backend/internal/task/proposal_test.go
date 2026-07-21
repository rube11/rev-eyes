package task

import (
	"context"
	"errors"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type resolverFunc func(context.Context, tool.Scope, Status) (bool, error)

func (f resolverFunc) ResolvePending(
	ctx context.Context,
	scope tool.Scope,
	status Status,
) (bool, error) {
	return f(ctx, scope, status)
}

func TestConfirmerAcceptsPendingReminder(t *testing.T) {
	t.Parallel()

	wantScope := tool.Scope{UserID: "user-1", SessionID: "session-1"}
	confirmer, err := NewConfirmer(resolverFunc(func(
		_ context.Context,
		scope tool.Scope,
		status Status,
	) (bool, error) {
		if scope != wantScope || status != StatusAccepted {
			t.Fatalf("ResolvePending() scope = %#v, status = %q", scope, status)
		}
		return true, nil
	}))
	if err != nil {
		t.Fatalf("NewConfirmer() error = %v", err)
	}

	response, handled, err := confirmer.Confirm(
		context.Background(),
		wantScope,
		"Yes please!",
	)
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if !handled || response != "Okay, I saved that reminder." {
		t.Fatalf("Confirm() response = %q, handled = %v", response, handled)
	}
}

func TestConfirmerRejectsPendingReminder(t *testing.T) {
	t.Parallel()

	confirmer, err := NewConfirmer(resolverFunc(func(
		_ context.Context,
		_ tool.Scope,
		status Status,
	) (bool, error) {
		if status != StatusRejected {
			t.Fatalf("status = %q", status)
		}
		return true, nil
	}))
	if err != nil {
		t.Fatalf("NewConfirmer() error = %v", err)
	}

	response, handled, err := confirmer.Confirm(context.Background(), tool.Scope{}, "Nope.")
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if !handled || response != "Okay, I won't create that reminder." {
		t.Fatalf("Confirm() response = %q, handled = %v", response, handled)
	}
}

func TestConfirmerIgnoresAmbiguousSpeech(t *testing.T) {
	t.Parallel()

	confirmer, err := NewConfirmer(resolverFunc(func(
		context.Context,
		tool.Scope,
		Status,
	) (bool, error) {
		t.Fatal("ResolvePending() was called")
		return false, nil
	}))
	if err != nil {
		t.Fatalf("NewConfirmer() error = %v", err)
	}

	response, handled, err := confirmer.Confirm(
		context.Background(),
		tool.Scope{},
		"Yes, but make it next week",
	)
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if handled || response != "" {
		t.Fatalf("Confirm() response = %q, handled = %v", response, handled)
	}
}

func TestConfirmerLeavesAnswerUnhandledWithoutPendingProposal(t *testing.T) {
	t.Parallel()

	confirmer, err := NewConfirmer(resolverFunc(func(
		context.Context,
		tool.Scope,
		Status,
	) (bool, error) {
		return false, nil
	}))
	if err != nil {
		t.Fatalf("NewConfirmer() error = %v", err)
	}

	response, handled, err := confirmer.Confirm(context.Background(), tool.Scope{}, "yes")
	if err != nil || handled || response != "" {
		t.Fatalf("Confirm() response = %q, handled = %v, error = %v", response, handled, err)
	}
}

func TestNewConfirmerRequiresResolver(t *testing.T) {
	t.Parallel()

	if _, err := NewConfirmer(nil); !errors.Is(err, ErrResolverRequired) {
		t.Fatalf("NewConfirmer(nil) error = %v", err)
	}
}
