package watch

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrRepositoryRequired = errors.New("watch repository is required")
	ErrSearcherRequired   = errors.New("watch searcher is required")
	ErrNotifierRequired   = errors.New("watch notifier is required")
)

type Repository interface {
	ClaimScheduled(context.Context, string) (Watch, bool, error)
	Record(context.Context, Watch, []Item) (bool, error)
}

type Searcher interface {
	Search(context.Context, string) ([]Item, error)
}

type PendingNotifier interface {
	Flush(context.Context, string) error
}

// SearchFunc adapts a search function into a Searcher.
type SearchFunc func(context.Context, string) ([]Item, error)

func (f SearchFunc) Search(ctx context.Context, query string) ([]Item, error) {
	return f(ctx, query)
}

// Dispatcher checks one scheduled watch and flushes a newly queued update.
type Dispatcher struct {
	repository Repository
	searcher   Searcher
	notifier   PendingNotifier
}

func NewDispatcher(
	repository Repository,
	searcher Searcher,
	notifier PendingNotifier,
) (*Dispatcher, error) {
	if repository == nil {
		return nil, ErrRepositoryRequired
	}
	if searcher == nil {
		return nil, ErrSearcherRequired
	}
	if notifier == nil {
		return nil, ErrNotifierRequired
	}
	return &Dispatcher{repository: repository, searcher: searcher, notifier: notifier}, nil
}

func (d *Dispatcher) RunResource(ctx context.Context, resourceID string) error {
	claimed, exists, err := d.repository.ClaimScheduled(ctx, resourceID)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	items, err := d.searcher.Search(ctx, claimed.Query)
	if err != nil {
		return fmt.Errorf("search watch %s: %w", claimed.ID, err)
	}
	notify, err := d.repository.Record(ctx, claimed, items)
	if err != nil {
		return fmt.Errorf("record watch %s: %w", claimed.ID, err)
	}
	if !notify {
		return nil
	}
	if err := d.notifier.Flush(ctx, claimed.UserID); err != nil {
		return fmt.Errorf("deliver watch update: %w", err)
	}
	return nil
}
