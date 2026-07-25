package reminder

import (
	"context"
	"errors"
	"testing"
)

type dueRepositoryFunc func(context.Context, string) (string, bool, error)

func (f dueRepositoryFunc) EnqueueScheduled(
	ctx context.Context,
	resourceID string,
) (string, bool, error) {
	return f(ctx, resourceID)
}

type notifierFunc func(context.Context, string) error

func (f notifierFunc) Flush(ctx context.Context, userID string) error {
	return f(ctx, userID)
}

func TestDispatcherEnqueuesAndDeliversScheduledReminder(t *testing.T) {
	t.Parallel()

	var delivered string
	dispatcher, err := NewDispatcher(
		dueRepositoryFunc(func(_ context.Context, resourceID string) (string, bool, error) {
			if resourceID != "reminder-1" {
				t.Fatalf("resource ID = %q", resourceID)
			}
			return "user-1", true, nil
		}),
		notifierFunc(func(_ context.Context, userID string) error {
			delivered = userID
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewDispatcher() error = %v", err)
	}
	if err := dispatcher.RunResource(context.Background(), "reminder-1"); err != nil {
		t.Fatalf("RunResource() error = %v", err)
	}
	if delivered != "user-1" {
		t.Fatalf("delivered = %q", delivered)
	}
}

func TestDispatcherIgnoresInactiveScheduledReminder(t *testing.T) {
	t.Parallel()

	dispatcher, _ := NewDispatcher(
		dueRepositoryFunc(func(context.Context, string) (string, bool, error) {
			return "", false, nil
		}),
		notifierFunc(func(context.Context, string) error {
			t.Fatal("Flush() was called")
			return nil
		}),
	)
	if err := dispatcher.RunResource(context.Background(), "reminder-1"); err != nil {
		t.Fatalf("RunResource() error = %v", err)
	}
}

func TestDispatcherStopsWhenEnqueueFails(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("database unavailable")
	dispatcher, _ := NewDispatcher(
		dueRepositoryFunc(func(context.Context, string) (string, bool, error) {
			return "", false, wantErr
		}),
		notifierFunc(func(context.Context, string) error {
			t.Fatal("Flush() was called")
			return nil
		}),
	)
	if err := dispatcher.RunResource(context.Background(), "reminder-1"); !errors.Is(err, wantErr) {
		t.Fatalf("RunResource() error = %v", err)
	}
}

func TestNewDispatcherRequiresDependencies(t *testing.T) {
	t.Parallel()

	repository := dueRepositoryFunc(func(context.Context, string) (string, bool, error) {
		return "", false, nil
	})
	notifier := notifierFunc(func(context.Context, string) error { return nil })
	if _, err := NewDispatcher(nil, notifier); !errors.Is(err, ErrDueRepositoryRequired) {
		t.Fatalf("NewDispatcher(nil, notifier) error = %v", err)
	}
	if _, err := NewDispatcher(repository, nil); !errors.Is(err, ErrNotifierRequired) {
		t.Fatalf("NewDispatcher(repository, nil) error = %v", err)
	}
}
