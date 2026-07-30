package registration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const registrationLease = time.Minute

var (
	ErrDatabaseRequired       = errors.New("schedule registration database is required")
	ErrRegistrationIDRequired = errors.New("schedule registration ID is required")
)

type Repository interface {
	Claim(context.Context, int) ([]Registration, error)
	MarkRegistered(context.Context, string) error
	MarkFailed(context.Context, string, time.Time, string) error
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, ErrDatabaseRequired
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Claim(ctx context.Context, limit int) ([]Registration, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(
		ctx,
		`with selected as (
		     select id
		     from public.schedule_registrations
		     where registered_at is null
		       and next_attempt_at <= statement_timestamp()
		       and (
		           locked_until is null
		           or locked_until <= statement_timestamp()
		       )
		     order by next_attempt_at, created_at, id
		     limit $1
		     for update skip locked
		 )
		 update public.schedule_registrations as registration
		 set attempts = registration.attempts + 1,
		     locked_until = statement_timestamp() + make_interval(secs => $2)
		 from selected
		 where registration.id = selected.id
		 returning
		     registration.id::text,
		     registration.operation,
		     registration.kind,
		     registration.resource_id::text,
		     registration.schedule_at,
		     coalesce(registration.interval_minutes, 0),
		     registration.end_at,
		     registration.attempts`,
		limit,
		int(registrationLease.Seconds()),
	)
	if err != nil {
		return nil, fmt.Errorf("claim schedule registrations: %w", err)
	}
	defer rows.Close()

	var registrations []Registration
	for rows.Next() {
		var registration Registration
		if err := rows.Scan(
			&registration.ID,
			&registration.Operation,
			&registration.Kind,
			&registration.ResourceID,
			&registration.ScheduleAt,
			&registration.IntervalMinutes,
			&registration.EndAt,
			&registration.Attempts,
		); err != nil {
			return nil, fmt.Errorf("scan schedule registration: %w", err)
		}
		registrations = append(registrations, registration)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read schedule registrations: %w", err)
	}
	return registrations, nil
}

func (s *Store) MarkRegistered(ctx context.Context, registrationID string) error {
	registrationID = strings.TrimSpace(registrationID)
	if registrationID == "" {
		return ErrRegistrationIDRequired
	}
	_, err := s.pool.Exec(
		ctx,
		`update public.schedule_registrations
		 set registered_at = statement_timestamp(),
		     locked_until = null,
		     last_error = null
		 where id = $1::uuid
		   and registered_at is null`,
		registrationID,
	)
	if err != nil {
		return fmt.Errorf("mark schedule registered: %w", err)
	}
	return nil
}

func (s *Store) MarkFailed(
	ctx context.Context,
	registrationID string,
	retryAt time.Time,
	message string,
) error {
	registrationID = strings.TrimSpace(registrationID)
	if registrationID == "" {
		return ErrRegistrationIDRequired
	}
	message = strings.TrimSpace(message)
	if len(message) > 1000 {
		message = message[:1000]
	}
	_, err := s.pool.Exec(
		ctx,
		`update public.schedule_registrations
		 set next_attempt_at = $2,
		     locked_until = null,
		     last_error = $3
		 where id = $1::uuid
		   and registered_at is null`,
		registrationID,
		retryAt.UTC(),
		message,
	)
	if err != nil {
		return fmt.Errorf("record schedule registration failure: %w", err)
	}
	return nil
}
