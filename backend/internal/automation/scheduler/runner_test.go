package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"
)

type resourceRunnerFunc func(context.Context, string) error

func (f resourceRunnerFunc) RunResource(ctx context.Context, resourceID string) error {
	return f(ctx, resourceID)
}

type repositoryStub struct {
	events        []ScheduledEvent
	enqueued      []ScheduledEvent
	processed     []string
	failed        []string
	abandoned     []string
	purgeCalls    int
	claimReturned bool
}

func (r *repositoryStub) Enqueue(_ context.Context, event ScheduledEvent) error {
	r.enqueued = append(r.enqueued, event)
	return nil
}

func (r *repositoryStub) Claim(context.Context, int) ([]ScheduledEvent, error) {
	if r.claimReturned {
		return nil, nil
	}
	r.claimReturned = true
	return r.events, nil
}

func (r *repositoryStub) MarkProcessed(_ context.Context, id string) error {
	r.processed = append(r.processed, id)
	return nil
}

func (r *repositoryStub) MarkFailed(
	_ context.Context,
	id string,
	_ time.Time,
	_ string,
) error {
	r.failed = append(r.failed, id)
	return nil
}

func (r *repositoryStub) MarkAbandoned(
	_ context.Context,
	id string,
	_ string,
) error {
	r.abandoned = append(r.abandoned, id)
	return nil
}

func (r *repositoryStub) PurgeTerminal(context.Context, time.Time) error {
	r.purgeCalls++
	return nil
}

func TestDispatcherProcessesEachResourceWithItsRunner(t *testing.T) {
	t.Parallel()

	repository := &repositoryStub{events: []ScheduledEvent{
		{ID: "event-1", Job: JobReminder, ResourceID: "reminder-1", Attempts: 1},
		{ID: "event-2", Job: JobWatch, ResourceID: "watch-1", Attempts: 1},
	}}
	var reminderID, watchID string
	dispatcher, err := NewDispatcher(
		repository,
		resourceRunnerFunc(func(_ context.Context, id string) error {
			reminderID = id
			return nil
		}),
		resourceRunnerFunc(func(_ context.Context, id string) error {
			watchID = id
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewDispatcher() error = %v", err)
	}

	if err := dispatcher.drain(context.Background()); err != nil {
		t.Fatalf("drain() error = %v", err)
	}
	if reminderID != "reminder-1" || watchID != "watch-1" {
		t.Fatalf("reminder = %q, watch = %q", reminderID, watchID)
	}
	if len(repository.processed) != 2 {
		t.Fatalf("processed = %#v", repository.processed)
	}
	if repository.purgeCalls != 1 {
		t.Fatalf("purge calls = %d", repository.purgeCalls)
	}
}

func TestDispatcherRetriesFailedWork(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("search unavailable")
	repository := &repositoryStub{events: []ScheduledEvent{
		{ID: "event-1", Job: JobWatch, ResourceID: "watch-1", Attempts: 1},
	}}
	dispatcher, _ := NewDispatcher(
		repository,
		resourceRunnerFunc(func(context.Context, string) error { return nil }),
		resourceRunnerFunc(func(context.Context, string) error { return wantErr }),
	)

	if err := dispatcher.drain(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("drain() error = %v", err)
	}
	if len(repository.failed) != 1 || repository.failed[0] != "event-1" {
		t.Fatalf("failed = %#v", repository.failed)
	}
	if len(repository.processed) != 0 {
		t.Fatalf("processed = %#v", repository.processed)
	}
}

func TestDispatcherBoundsWatchRetries(t *testing.T) {
	t.Parallel()

	repository := &repositoryStub{events: []ScheduledEvent{
		{
			ID:         "event-1",
			Job:        JobWatch,
			ResourceID: "watch-1",
			Attempts:   maxWatchAttempts,
		},
	}}
	dispatcher, _ := NewDispatcher(
		repository,
		resourceRunnerFunc(func(context.Context, string) error { return nil }),
		resourceRunnerFunc(func(context.Context, string) error {
			return errors.New("search unavailable")
		}),
	)

	_ = dispatcher.drain(context.Background())
	if len(repository.abandoned) != 1 || repository.abandoned[0] != "event-1" {
		t.Fatalf("abandoned = %#v", repository.abandoned)
	}
	if len(repository.failed) != 0 {
		t.Fatalf("failed = %#v", repository.failed)
	}
}

func TestDispatcherEnqueuePersistsBeforeWake(t *testing.T) {
	t.Parallel()

	repository := &repositoryStub{}
	dispatcher, _ := NewDispatcher(
		repository,
		resourceRunnerFunc(func(context.Context, string) error { return nil }),
		resourceRunnerFunc(func(context.Context, string) error { return nil }),
	)
	event := ScheduledEvent{ID: validEventID, Job: JobWatch, ResourceID: validResourceID}
	if err := dispatcher.Enqueue(context.Background(), event); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if len(repository.enqueued) != 1 || repository.enqueued[0] != event {
		t.Fatalf("enqueued = %#v", repository.enqueued)
	}
	select {
	case <-dispatcher.triggers:
	default:
		t.Fatal("dispatcher was not woken")
	}
}

func TestNewDispatcherRequiresDependencies(t *testing.T) {
	t.Parallel()

	repository := &repositoryStub{}
	runner := resourceRunnerFunc(func(context.Context, string) error { return nil })
	if _, err := NewDispatcher(nil, runner, runner); !errors.Is(err, ErrRepositoryRequired) {
		t.Fatalf("missing repository error = %v", err)
	}
	if _, err := NewDispatcher(repository, nil, runner); !errors.Is(err, ErrReminderRunnerRequired) {
		t.Fatalf("missing reminder runner error = %v", err)
	}
	if _, err := NewDispatcher(repository, runner, nil); !errors.Is(err, ErrWatchRunnerRequired) {
		t.Fatalf("missing watch runner error = %v", err)
	}
}
