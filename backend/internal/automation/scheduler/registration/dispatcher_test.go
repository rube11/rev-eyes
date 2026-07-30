package registration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type repositoryStub struct {
	mu         sync.Mutex
	pending    []Registration
	registered []string
	failed     []string
}

func (r *repositoryStub) Claim(context.Context, int) ([]Registration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pending) == 0 {
		return nil, nil
	}
	claimed := append([]Registration(nil), r.pending...)
	r.pending = nil
	return claimed, nil
}

func (r *repositoryStub) MarkRegistered(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.registered = append(r.registered, id)
	return nil
}

func (r *repositoryStub) MarkFailed(
	_ context.Context,
	id string,
	_ time.Time,
	_ string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failed = append(r.failed, id)
	return nil
}

type registrarFunc func(context.Context, Registration) error

func (f registrarFunc) Register(
	ctx context.Context,
	registration Registration,
) error {
	return f(ctx, registration)
}

func TestDispatcherRegistersPendingSchedules(t *testing.T) {
	t.Parallel()

	repository := &repositoryStub{
		pending: []Registration{{
			ID:         "registration-id",
			Operation:  OperationRegister,
			Kind:       KindReminder,
			ResourceID: "resource-id",
			ScheduleAt: timePointer(time.Now().Add(time.Hour)),
			Attempts:   1,
		}},
	}
	dispatcher, err := NewDispatcher(
		repository,
		registrarFunc(func(context.Context, Registration) error { return nil }),
	)
	if err != nil {
		t.Fatalf("NewDispatcher() error = %v", err)
	}
	dispatcher.drain(context.Background())

	repository.mu.Lock()
	defer repository.mu.Unlock()
	if len(repository.registered) != 1 || repository.registered[0] != "registration-id" {
		t.Fatalf("registered = %v", repository.registered)
	}
}

func TestDispatcherRecordsRegistrationFailure(t *testing.T) {
	t.Parallel()

	repository := &repositoryStub{
		pending: []Registration{{
			ID:         "registration-id",
			Operation:  OperationRegister,
			Kind:       KindReminder,
			ResourceID: "resource-id",
			ScheduleAt: timePointer(time.Now().Add(time.Hour)),
			Attempts:   1,
		}},
	}
	dispatcher, _ := NewDispatcher(
		repository,
		registrarFunc(func(context.Context, Registration) error {
			return errors.New("failed")
		}),
	)
	dispatcher.drain(context.Background())

	repository.mu.Lock()
	defer repository.mu.Unlock()
	if len(repository.failed) != 1 || repository.failed[0] != "registration-id" {
		t.Fatalf("failed = %v", repository.failed)
	}
}

func TestRetryDelayIsBounded(t *testing.T) {
	t.Parallel()

	if got := retryDelay(1); got != 5*time.Second {
		t.Fatalf("retryDelay(1) = %s", got)
	}
	if got := retryDelay(100); got != maxRetryDelay {
		t.Fatalf("retryDelay(100) = %s", got)
	}
}

func timePointer(value time.Time) *time.Time {
	return &value
}
