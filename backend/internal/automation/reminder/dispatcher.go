package reminder

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var (
	ErrDueRepositoryRequired = errors.New("due reminder repository is required")
	ErrNotifierRequired      = errors.New("due reminder notifier is required")
	ErrReminderNotDue        = errors.New("scheduled reminder is not due yet")
)

type DueRepository interface {
	EnqueueScheduled(context.Context, string) (string, bool, error)
}

type PendingNotifier interface {
	Flush(context.Context, string) error
}

// Dispatcher moves one scheduled reminder into the notification outbox.
type Dispatcher struct {
	repository DueRepository
	notifier   PendingNotifier
}

func NewDispatcher(repository DueRepository, notifier PendingNotifier) (*Dispatcher, error) {
	if repository == nil {
		return nil, ErrDueRepositoryRequired
	}
	if notifier == nil {
		return nil, ErrNotifierRequired
	}
	return &Dispatcher{repository: repository, notifier: notifier}, nil
}

func (d *Dispatcher) RunResource(ctx context.Context, resourceID string) error {
	userID, exists, err := d.repository.EnqueueScheduled(ctx, resourceID)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if err := d.notifier.Flush(ctx, userID); err != nil {
		return fmt.Errorf("deliver due reminder: %w", err)
	}
	return nil
}

// EnqueueScheduled atomically persists one due reminder notification.
func (s *Store) EnqueueScheduled(
	ctx context.Context,
	resourceID string,
) (string, bool, error) {
	var userID string
	err := s.pool.QueryRow(
		ctx,
		`with claimed as (
		     update public.task_proposals
		     set enqueued_at = statement_timestamp()
		     where id = $1::uuid
		       and status = 'accepted'
		       and due_at <= statement_timestamp()
		       and enqueued_at is null
		     returning user_id, title
		 ),
		 created as (
		     insert into public.notifications (user_id, text)
		     select user_id, 'Reminder: ' || title
		     from claimed
		     returning user_id
		 )
		 select user_id::text
		 from created`,
		resourceID,
	).Scan(&userID)
	if err == nil {
		return userID, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, fmt.Errorf("enqueue scheduled reminder: %w", err)
	}

	var (
		isEarly    bool
		isEnqueued bool
	)
	err = s.pool.QueryRow(
		ctx,
		`select
		     user_id::text,
		     due_at > statement_timestamp(),
		     enqueued_at is not null
		 from public.task_proposals
		 where id = $1::uuid
		   and status = 'accepted'`,
		resourceID,
	).Scan(&userID, &isEarly, &isEnqueued)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("load scheduled reminder: %w", err)
	}
	if isEarly {
		return "", false, ErrReminderNotDue
	}
	if !isEnqueued {
		return "", false, errors.New("due reminder was not enqueued")
	}
	return userID, true, nil
}
