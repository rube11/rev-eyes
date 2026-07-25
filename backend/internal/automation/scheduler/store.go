package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const eventLease = 3 * time.Minute

var (
	ErrDatabaseRequired = errors.New("scheduled event database is required")
	ErrEventIDRequired  = errors.New("scheduled event ID is required")
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, ErrDatabaseRequired
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Enqueue(ctx context.Context, event ScheduledEvent) error {
	if err := event.validate(); err != nil {
		return err
	}
	_, err := s.pool.Exec(
		ctx,
		`insert into public.scheduled_job_events (id, kind, resource_id)
		 values ($1::uuid, $2, $3::uuid)
		 on conflict (id) do nothing`,
		event.ID,
		string(event.Job),
		event.ResourceID,
	)
	if err != nil {
		return fmt.Errorf("enqueue scheduled event: %w", err)
	}
	return nil
}

func (s *Store) Claim(ctx context.Context, limit int) ([]ScheduledEvent, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(
		ctx,
		`with selected as (
		     select id
		     from public.scheduled_job_events
		     where processed_at is null
		       and abandoned_at is null
		       and next_attempt_at <= statement_timestamp()
		       and (
		           locked_until is null
		           or locked_until <= statement_timestamp()
		       )
		     order by next_attempt_at, received_at, id
		     limit $1
		     for update skip locked
		 )
		 update public.scheduled_job_events as event
		 set attempts = event.attempts + 1,
		     locked_until = statement_timestamp() + make_interval(secs => $2)
		 from selected
		 where event.id = selected.id
		 returning
		     event.id::text,
		     event.kind,
		     event.resource_id::text,
		     event.attempts`,
		limit,
		int(eventLease.Seconds()),
	)
	if err != nil {
		return nil, fmt.Errorf("claim scheduled events: %w", err)
	}
	defer rows.Close()

	var events []ScheduledEvent
	for rows.Next() {
		var event ScheduledEvent
		if err := rows.Scan(
			&event.ID,
			&event.Job,
			&event.ResourceID,
			&event.Attempts,
		); err != nil {
			return nil, fmt.Errorf("scan scheduled event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read scheduled events: %w", err)
	}
	return events, nil
}

func (s *Store) MarkProcessed(ctx context.Context, eventID string) error {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return ErrEventIDRequired
	}
	_, err := s.pool.Exec(
		ctx,
		`update public.scheduled_job_events
		 set processed_at = statement_timestamp(),
		     locked_until = null,
		     last_error = null
		 where id = $1::uuid
		   and processed_at is null
		   and abandoned_at is null`,
		eventID,
	)
	if err != nil {
		return fmt.Errorf("mark scheduled event processed: %w", err)
	}
	return nil
}

func (s *Store) MarkFailed(
	ctx context.Context,
	eventID string,
	retryAt time.Time,
	message string,
) error {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return ErrEventIDRequired
	}
	_, err := s.pool.Exec(
		ctx,
		`update public.scheduled_job_events
		 set next_attempt_at = $2,
		     locked_until = null,
		     last_error = $3
		 where id = $1::uuid
		   and processed_at is null
		   and abandoned_at is null`,
		eventID,
		retryAt.UTC(),
		truncateError(message),
	)
	if err != nil {
		return fmt.Errorf("record scheduled event failure: %w", err)
	}
	return nil
}

func (s *Store) MarkAbandoned(
	ctx context.Context,
	eventID string,
	message string,
) error {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return ErrEventIDRequired
	}
	_, err := s.pool.Exec(
		ctx,
		`update public.scheduled_job_events
		 set abandoned_at = statement_timestamp(),
		     locked_until = null,
		     last_error = $2
		 where id = $1::uuid
		   and processed_at is null
		   and abandoned_at is null`,
		eventID,
		truncateError(message),
	)
	if err != nil {
		return fmt.Errorf("abandon scheduled event: %w", err)
	}
	return nil
}

func (s *Store) PurgeTerminal(ctx context.Context, before time.Time) error {
	_, err := s.pool.Exec(
		ctx,
		`delete from public.scheduled_job_events
		 where coalesce(processed_at, abandoned_at) < $1`,
		before.UTC(),
	)
	if err != nil {
		return fmt.Errorf("purge terminal scheduled events: %w", err)
	}
	return nil
}

func truncateError(message string) string {
	runes := []rune(strings.TrimSpace(message))
	if len(runes) > 1000 {
		runes = runes[:1000]
	}
	return string(runes)
}
