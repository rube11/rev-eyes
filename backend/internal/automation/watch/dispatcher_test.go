package watch

import (
	"context"
	"errors"
	"testing"
	"time"
)

type repositoryStub struct {
	watch    Watch
	exists   bool
	recorded []Watch
	notify   bool
}

func (r *repositoryStub) ClaimScheduled(
	_ context.Context,
	resourceID string,
) (Watch, bool, error) {
	if r.watch.ID != "" && resourceID != r.watch.ID {
		return Watch{}, false, errors.New("unexpected resource ID")
	}
	return r.watch, r.exists, nil
}

func (r *repositoryStub) Record(
	_ context.Context,
	claimed Watch,
	_ []Item,
) (bool, error) {
	r.recorded = append(r.recorded, claimed)
	return r.notify, nil
}

type searcherFunc func(context.Context, string) ([]Item, error)

func (f searcherFunc) Search(ctx context.Context, query string) ([]Item, error) {
	return f(ctx, query)
}

type notifierStub struct {
	users []string
}

func (n *notifierStub) Flush(_ context.Context, userID string) error {
	n.users = append(n.users, userID)
	return nil
}

func TestDispatcherChecksScheduledWatchAndFlushesUpdate(t *testing.T) {
	t.Parallel()

	claimed := Watch{
		ID:          "watch-1",
		UserID:      "user-1",
		Query:       "first",
		NextCheckAt: time.Now(),
	}
	repository := &repositoryStub{
		watch:  claimed,
		exists: true,
		notify: true,
	}
	notifier := &notifierStub{}
	dispatcher, err := NewDispatcher(
		repository,
		searcherFunc(func(_ context.Context, query string) ([]Item, error) {
			return []Item{{Title: query, URL: "https://example.com/first"}}, nil
		}),
		notifier,
	)
	if err != nil {
		t.Fatalf("NewDispatcher() error = %v", err)
	}

	if err := dispatcher.RunResource(context.Background(), "watch-1"); err != nil {
		t.Fatalf("RunResource() error = %v", err)
	}
	if len(repository.recorded) != 1 || repository.recorded[0].ID != claimed.ID {
		t.Fatalf("recorded = %#v", repository.recorded)
	}
	if len(notifier.users) != 1 || notifier.users[0] != "user-1" {
		t.Fatalf("notified users = %#v", notifier.users)
	}
}

func TestDispatcherLeavesFailedSearchRetryable(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("search unavailable")
	repository := &repositoryStub{
		watch:  Watch{ID: "watch-1", UserID: "user-1", Query: "first"},
		exists: true,
	}
	dispatcher, _ := NewDispatcher(
		repository,
		searcherFunc(func(context.Context, string) ([]Item, error) {
			return nil, wantErr
		}),
		&notifierStub{},
	)

	if err := dispatcher.RunResource(context.Background(), "watch-1"); !errors.Is(err, wantErr) {
		t.Fatalf("RunResource() error = %v", err)
	}
	if len(repository.recorded) != 0 {
		t.Fatalf("recorded = %#v", repository.recorded)
	}
}

func TestDispatcherIgnoresInactiveWatch(t *testing.T) {
	t.Parallel()

	dispatcher, _ := NewDispatcher(
		&repositoryStub{},
		searcherFunc(func(context.Context, string) ([]Item, error) {
			t.Fatal("Search() was called")
			return nil, nil
		}),
		&notifierStub{},
	)
	if err := dispatcher.RunResource(context.Background(), "watch-1"); err != nil {
		t.Fatalf("RunResource() error = %v", err)
	}
}

func TestNewDispatcherRequiresDependencies(t *testing.T) {
	t.Parallel()

	repository := &repositoryStub{}
	searcher := searcherFunc(func(context.Context, string) ([]Item, error) { return nil, nil })
	notifier := &notifierStub{}

	if _, err := NewDispatcher(nil, searcher, notifier); !errors.Is(err, ErrRepositoryRequired) {
		t.Fatalf("nil repository error = %v", err)
	}
	if _, err := NewDispatcher(repository, nil, notifier); !errors.Is(err, ErrSearcherRequired) {
		t.Fatalf("nil searcher error = %v", err)
	}
	if _, err := NewDispatcher(repository, searcher, nil); !errors.Is(err, ErrNotifierRequired) {
		t.Fatalf("nil notifier error = %v", err)
	}
}
