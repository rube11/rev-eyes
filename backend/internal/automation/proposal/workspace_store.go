package proposal

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrAutomationNotFound = errors.New("automation was not found")
	ErrKindInvalid        = errors.New("automation kind is invalid")
	ErrReminderTimePassed = errors.New("reminder time has already passed")
	ErrResourceIDInvalid  = errors.New("automation resource ID is invalid")
	ErrUserIDInvalid      = errors.New("automation user ID is invalid")
	ErrWatchExpired       = errors.New("watch has already expired")
	ErrWatchLimitReached  = errors.New("active watch limit reached")
	workspaceUUIDPattern  = regexp.MustCompile(
		`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
	)
)

// ResolveByID resolves an owner-scoped reminder or watch and atomically queues
// its schedule registration when accepted.
func (s *Store) ResolveByID(
	ctx context.Context,
	userID string,
	kind Kind,
	resourceID string,
	status Status,
) (Resolution, error) {
	userID, resourceID, err := validateWorkspaceCommand(userID, kind, resourceID)
	if err != nil {
		return Resolution{}, err
	}
	if status != StatusAccepted && status != StatusRejected {
		return Resolution{}, ErrStatusInvalid
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Resolution{}, fmt.Errorf("begin workspace proposal resolution: %w", err)
	}
	defer tx.Rollback(ctx)

	var resolvedAt time.Time
	if err := tx.QueryRow(ctx, `select statement_timestamp()`).Scan(&resolvedAt); err != nil {
		return Resolution{}, fmt.Errorf("read proposal resolution time: %w", err)
	}

	var resolution Resolution
	switch kind {
	case KindReminder:
		resolution, err = resolveReminderByID(
			ctx,
			tx,
			userID,
			resourceID,
			status,
			resolvedAt,
		)
	case KindWatch:
		resolution, err = resolveWatchByID(
			ctx,
			tx,
			userID,
			resourceID,
			status,
			resolvedAt,
		)
	}
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) &&
			databaseError.ConstraintName == "watches_active_limit_check" {
			return Resolution{}, ErrWatchLimitReached
		}
		return Resolution{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Resolution{}, fmt.Errorf("commit workspace proposal resolution: %w", err)
	}
	return resolution, nil
}

// DeleteByID deletes an owner-scoped automation. Accepted reminders and active
// watches replace their existing outbox row with a durable cancellation.
func (s *Store) DeleteByID(
	ctx context.Context,
	userID string,
	kind Kind,
	resourceID string,
) (bool, error) {
	userID, resourceID, err := validateWorkspaceCommand(userID, kind, resourceID)
	if err != nil {
		return false, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin workspace automation deletion: %w", err)
	}
	defer tx.Rollback(ctx)

	var resourceStatus string
	switch kind {
	case KindReminder:
		err = tx.QueryRow(
			ctx,
			`select status
			 from public.task_proposals
			 where id = $2::uuid
			   and user_id = $1::uuid
			 for update`,
			userID,
			resourceID,
		).Scan(&resourceStatus)
	case KindWatch:
		err = tx.QueryRow(
			ctx,
			`select status
			 from public.watches
			 where id = $2::uuid
			   and user_id = $1::uuid
			 for update`,
			userID,
			resourceID,
		).Scan(&resourceStatus)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrAutomationNotFound
	}
	if err != nil {
		return false, fmt.Errorf("lock workspace automation: %w", err)
	}

	queueCancellation :=
		(kind == KindReminder && resourceStatus == string(StatusAccepted)) ||
			(kind == KindWatch && resourceStatus == "active")
	if queueCancellation {
		if _, err := tx.Exec(
			ctx,
			`insert into public.schedule_registrations (
			     id,
			     operation,
			     kind,
			     resource_id
			 )
			 values (gen_random_uuid(), 'cancel', $1, $2::uuid)
			 on conflict (kind, resource_id) do update
			 set id = gen_random_uuid(),
			     operation = 'cancel',
			     schedule_at = null,
			     interval_minutes = null,
			     end_at = null,
			     attempts = 0,
			     next_attempt_at = greatest(
			         statement_timestamp(),
			         coalesce(
			             schedule_registrations.locked_until,
			             statement_timestamp()
			         )
			     ),
			     locked_until = null,
			     registered_at = null,
			     last_error = null,
			     created_at = statement_timestamp()`,
			string(kind),
			resourceID,
		); err != nil {
			return false, fmt.Errorf("queue schedule cancellation: %w", err)
		}
	} else if _, err := tx.Exec(
		ctx,
		`delete from public.schedule_registrations
		 where kind = $1
		   and resource_id = $2::uuid`,
		string(kind),
		resourceID,
	); err != nil {
		return false, fmt.Errorf("delete unused schedule registration: %w", err)
	}

	if _, err := tx.Exec(
		ctx,
		`delete from public.scheduled_job_events
		 where kind = $1
		   and resource_id = $2::uuid`,
		string(kind),
		resourceID,
	); err != nil {
		return false, fmt.Errorf("delete queued schedule events: %w", err)
	}

	switch kind {
	case KindReminder:
		_, err = tx.Exec(
			ctx,
			`delete from public.task_proposals
			 where id = $2::uuid
			   and user_id = $1::uuid`,
			userID,
			resourceID,
		)
	case KindWatch:
		_, err = tx.Exec(
			ctx,
			`delete from public.watches
			 where id = $2::uuid
			   and user_id = $1::uuid`,
			userID,
			resourceID,
		)
	}
	if err != nil {
		return false, fmt.Errorf("delete workspace automation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit workspace automation deletion: %w", err)
	}
	return queueCancellation, nil
}

func resolveReminderByID(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	resourceID string,
	status Status,
	resolvedAt time.Time,
) (Resolution, error) {
	var dueAt time.Time
	err := tx.QueryRow(
		ctx,
		`select due_at
		 from public.task_proposals
		 where id = $2::uuid
		   and user_id = $1::uuid
		   and status = 'proposed'
		 for update`,
		userID,
		resourceID,
	).Scan(&dueAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Resolution{}, ErrAutomationNotFound
	}
	if err != nil {
		return Resolution{}, fmt.Errorf("lock reminder proposal: %w", err)
	}
	if status == StatusAccepted && !dueAt.After(resolvedAt) {
		return Resolution{}, ErrReminderTimePassed
	}
	if _, err := tx.Exec(
		ctx,
		`update public.task_proposals
		 set status = $3,
		     resolved_at = $4
		 where id = $2::uuid
		   and user_id = $1::uuid`,
		userID,
		resourceID,
		string(status),
		resolvedAt,
	); err != nil {
		return Resolution{}, fmt.Errorf("resolve reminder proposal: %w", err)
	}
	if status == StatusAccepted {
		if _, err := tx.Exec(
			ctx,
			`insert into public.schedule_registrations (
			     kind,
			     resource_id,
			     schedule_at
			 )
			 values ('reminder', $1::uuid, $2)
			 on conflict (kind, resource_id) do nothing`,
			resourceID,
			dueAt,
		); err != nil {
			return Resolution{}, fmt.Errorf("queue reminder registration: %w", err)
		}
	}
	return Resolution{Kind: KindReminder, Status: status}, nil
}

func resolveWatchByID(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	resourceID string,
	status Status,
	resolvedAt time.Time,
) (Resolution, error) {
	if status == StatusAccepted {
		if _, err := tx.Exec(
			ctx,
			`update public.watches
			 set status = 'expired'
			 where user_id = $1::uuid
			   and status = 'active'
			   and expires_at <= $2`,
			userID,
			resolvedAt,
		); err != nil {
			return Resolution{}, fmt.Errorf("expire completed watches: %w", err)
		}
	}

	var (
		intervalMinutes int
		expiresAt       time.Time
	)
	err := tx.QueryRow(
		ctx,
		`select interval_minutes, expires_at
		 from public.watches
		 where id = $2::uuid
		   and user_id = $1::uuid
		   and status = 'proposed'
		 for update`,
		userID,
		resourceID,
	).Scan(&intervalMinutes, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Resolution{}, ErrAutomationNotFound
	}
	if err != nil {
		return Resolution{}, fmt.Errorf("lock watch proposal: %w", err)
	}
	if status == StatusAccepted && !expiresAt.After(resolvedAt) {
		return Resolution{}, ErrWatchExpired
	}

	watchStatus := "rejected"
	var nextCheckAt *time.Time
	if status == StatusAccepted {
		watchStatus = "active"
		nextCheckAt = &resolvedAt
	}
	if _, err := tx.Exec(
		ctx,
		`update public.watches
		 set status = $3,
		     resolved_at = $4,
		     next_check_at = $5
		 where id = $2::uuid
		   and user_id = $1::uuid`,
		userID,
		resourceID,
		watchStatus,
		resolvedAt,
		nextCheckAt,
	); err != nil {
		return Resolution{}, fmt.Errorf("resolve watch proposal: %w", err)
	}
	if status == StatusAccepted {
		if _, err := tx.Exec(
			ctx,
			`insert into public.schedule_registrations (
			     kind,
			     resource_id,
			     interval_minutes,
			     end_at
			 )
			 values ('watch', $1::uuid, $2, $3)
			 on conflict (kind, resource_id) do nothing`,
			resourceID,
			intervalMinutes,
			expiresAt,
		); err != nil {
			return Resolution{}, fmt.Errorf("queue watch registration: %w", err)
		}
	}
	return Resolution{Kind: KindWatch, Status: status}, nil
}

func validateWorkspaceCommand(
	userID string,
	kind Kind,
	resourceID string,
) (string, string, error) {
	userID = strings.TrimSpace(userID)
	resourceID = strings.TrimSpace(resourceID)
	if !workspaceUUIDPattern.MatchString(userID) {
		return "", "", ErrUserIDInvalid
	}
	if kind != KindReminder && kind != KindWatch {
		return "", "", ErrKindInvalid
	}
	if !workspaceUUIDPattern.MatchString(resourceID) {
		return "", "", ErrResourceIDInvalid
	}
	return userID, resourceID, nil
}
