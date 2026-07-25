package proposal

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rube11/rev-eyes/backend/internal/automation/watch"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

var (
	ErrDatabaseRequired  = errors.New("proposal database is required")
	ErrScopeRequired     = errors.New("proposal scope is required")
	ErrUtteranceRequired = errors.New("proposal utterance is required")
	ErrStatusInvalid     = errors.New("proposal status is invalid")
)

// Store resolves the latest reminder or watch proposal in a conversation.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, ErrDatabaseRequired
	}
	return &Store{pool: pool}, nil
}

func (s *Store) ResolvePending(
	ctx context.Context,
	scope tool.Scope,
	status Status,
) (Resolution, bool, error) {
	scope.UserID = strings.TrimSpace(scope.UserID)
	scope.SessionID = strings.TrimSpace(scope.SessionID)
	if scope.UserID == "" || scope.SessionID == "" {
		return Resolution{}, false, ErrScopeRequired
	}
	scope.UtteranceID = strings.TrimSpace(scope.UtteranceID)
	if scope.UtteranceID == "" {
		return Resolution{}, false, ErrUtteranceRequired
	}
	if status != StatusAccepted && status != StatusRejected {
		return Resolution{}, false, ErrStatusInvalid
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Resolution{}, false, fmt.Errorf("begin proposal resolution: %w", err)
	}
	defer tx.Rollback(ctx)

	if status == StatusAccepted {
		if _, err := tx.Exec(
			ctx,
			`insert into public.watch_active_counts (user_id)
			 values ($1::uuid)
			 on conflict (user_id) do nothing`,
			scope.UserID,
		); err != nil {
			return Resolution{}, false, fmt.Errorf("prepare active watch counter: %w", err)
		}
		var activeCount int
		if err := tx.QueryRow(
			ctx,
			`select active_count
			 from public.watch_active_counts
			 where user_id = $1::uuid
			 for update`,
			scope.UserID,
		).Scan(&activeCount); err != nil {
			return Resolution{}, false, fmt.Errorf("lock active watch counter: %w", err)
		}
		if _, err := tx.Exec(
			ctx,
			`update public.watches
			 set status = 'expired'
			 where user_id = $1::uuid
			   and status = 'active'
			   and expires_at <= statement_timestamp()`,
			scope.UserID,
		); err != nil {
			return Resolution{}, false, fmt.Errorf("expire completed watches: %w", err)
		}
	}

	var (
		kind         Kind
		limitReached bool
	)
	err = tx.QueryRow(
		ctx,
		`with current_utterance as (
		     select id, created_at
		     from public.transcript_utterances
		     where id = $3::uuid
		       and user_id = $1::uuid
		       and session_id = $2::uuid
		       and speaker = 'user'
		 ),
		 pending as (
		     select
		         'reminder'::text as kind,
		         id,
		         user_id,
		         session_id,
		         source_utterance_id,
		         created_at
		     from public.task_proposals
		     where user_id = $1::uuid
		       and session_id = $2::uuid
		       and status = 'proposed'
		     union all
		     select
		         'watch'::text as kind,
		         id,
		         user_id,
		         session_id,
		         source_utterance_id,
		         created_at
		     from public.watches
		     where user_id = $1::uuid
		       and session_id = $2::uuid
		       and status = 'proposed'
		       and expires_at > statement_timestamp()
		 ),
		 eligible as (
		     select candidate.kind, candidate.id, candidate.created_at
		     from pending as candidate
		     join public.transcript_utterances as source
		       on source.id = candidate.source_utterance_id
		      and source.user_id = candidate.user_id
		      and source.session_id = candidate.session_id
		     cross join current_utterance as current
		     where (current.created_at, current.id) > (source.created_at, source.id)
		       and exists (
		           select 1
		           from public.transcript_utterances as prompt
		           where prompt.user_id = candidate.user_id
		             and prompt.session_id = candidate.session_id
		             and prompt.speaker = 'assistant'
		             and (prompt.created_at, prompt.id) > (source.created_at, source.id)
		             and (prompt.created_at, prompt.id) < (current.created_at, current.id)
		       )
		       and not exists (
		           select 1
		           from public.transcript_utterances as intervening
		           where intervening.user_id = candidate.user_id
		             and intervening.session_id = candidate.session_id
		             and intervening.speaker = 'user'
		             and (intervening.created_at, intervening.id) > (source.created_at, source.id)
		             and (intervening.created_at, intervening.id) < (current.created_at, current.id)
		       )
		 ),
		 latest as (
		     select kind, id
		     from eligible
		     order by created_at desc, id desc
		     limit 1
		 ),
		 decision as (
		     select
		         latest.kind,
		         latest.id,
		         latest.kind = 'watch'
		             and $4 = 'accepted'
		             and coalesce((
		                 select active_count
		                 from public.watch_active_counts
		                 where user_id = $1::uuid
		             ), 0) >= $5 as active_watch_limit_reached
		     from latest
		 ),
		 resolved_reminder as (
		     update public.task_proposals as candidate
		     set status = $4,
		         resolved_at = statement_timestamp()
		     from decision
		     where decision.kind = 'reminder'
		       and not decision.active_watch_limit_reached
		       and candidate.id = decision.id
		     returning candidate.id
		 ),
		 resolved_watch as (
		     update public.watches as candidate
		     set status = case when $4 = 'accepted' then 'active' else 'rejected' end,
		         resolved_at = statement_timestamp(),
		         next_check_at = case when $4 = 'accepted' then statement_timestamp() end
		     from decision
		     where decision.kind = 'watch'
		       and not decision.active_watch_limit_reached
		       and candidate.id = decision.id
		     returning candidate.id
		 ),
		 registration as (
		     insert into public.schedule_registrations (
		         kind,
		         resource_id,
		         schedule_at,
		         interval_minutes,
		         end_at
		     )
		     select
		         requested.kind,
		         requested.resource_id,
		         requested.schedule_at,
		         requested.interval_minutes,
		         requested.end_at
		     from (
		         select
		             'reminder'::text as kind,
		             candidate.id as resource_id,
		             candidate.due_at as schedule_at,
		             null::integer as interval_minutes,
		             null::timestamptz as end_at
		         from public.task_proposals as candidate
		         join resolved_reminder
		           on resolved_reminder.id = candidate.id
		         union all
		         select
		             'watch'::text as kind,
		             candidate.id as resource_id,
		             null::timestamptz as schedule_at,
		             candidate.interval_minutes,
		             candidate.expires_at as end_at
		         from public.watches as candidate
		         join resolved_watch
		           on resolved_watch.id = candidate.id
		     ) as requested
		     where $4 = 'accepted'
		     on conflict (kind, resource_id) do nothing
		     returning id
		 )
		 select decision.kind, decision.active_watch_limit_reached
		 from decision
		 left join lateral (
		     select count(*) as registration_count
		     from registration
		 ) as scheduled on true`,
		scope.UserID,
		scope.SessionID,
		scope.UtteranceID,
		string(status),
		watch.MaxActiveWatches,
	).Scan(&kind, &limitReached)
	if errors.Is(err, pgx.ErrNoRows) {
		return Resolution{}, false, nil
	}
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) &&
			databaseError.ConstraintName == "watches_active_limit_check" {
			return Resolution{
				Kind:                    KindWatch,
				Status:                  status,
				ActiveWatchLimitReached: true,
			}, true, nil
		}
		return Resolution{}, false, fmt.Errorf("resolve pending proposal: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Resolution{}, false, fmt.Errorf("commit proposal resolution: %w", err)
	}
	return Resolution{
		Kind:                    kind,
		Status:                  status,
		ActiveWatchLimitReached: limitReached,
	}, true, nil
}
