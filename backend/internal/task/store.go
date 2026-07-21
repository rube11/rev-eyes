package task

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

var (
	ErrDatabaseRequired  = errors.New("task database is required")
	ErrScopeRequired     = errors.New("task scope is required")
	ErrUtteranceRequired = errors.New("task utterance is required")
	ErrSessionInactive   = errors.New("task session is not active")
)

// Store persists task proposals within the authenticated application session.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, ErrDatabaseRequired
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Propose(
	ctx context.Context,
	scope tool.Scope,
	proposal Proposal,
) error {
	scope, err := validateScope(scope)
	if err != nil {
		return err
	}
	proposal = proposal.normalize()
	if err := proposal.validate(); err != nil {
		return err
	}

	result, err := s.pool.Exec(
		ctx,
		`insert into public.task_proposals (
		     user_id,
		     session_id,
		     source_utterance_id,
		     kind,
		     title,
		     schedule
		 )
		 select
		     utterance.user_id,
		     utterance.session_id,
		     utterance.id,
		     'reminder',
		     $4,
		     nullif($5, '')
		 from public.transcript_utterances as utterance
		 join public.sessions as session
		   on session.id = utterance.session_id
		  and session.user_id = utterance.user_id
		 where utterance.id = $3::uuid
		   and utterance.user_id = $1::uuid
		   and utterance.session_id = $2::uuid
		   and utterance.speaker = 'user'
		   and session.status = 'active'
		 on conflict (session_id) where status = 'proposed'
		 do update set
		     source_utterance_id = excluded.source_utterance_id,
		     kind = excluded.kind,
		     title = excluded.title,
		     schedule = excluded.schedule,
		     created_at = statement_timestamp()
		`,
		scope.UserID,
		scope.SessionID,
		scope.UtteranceID,
		proposal.Title,
		proposal.Schedule,
	)
	if err != nil {
		return fmt.Errorf("save task proposal: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrSessionInactive
	}
	return nil
}

func (s *Store) ResolvePending(
	ctx context.Context,
	scope tool.Scope,
	status Status,
) (bool, error) {
	scope, err := validateScope(scope)
	if err != nil {
		return false, err
	}
	if status != StatusAccepted && status != StatusRejected {
		return false, fmt.Errorf("%w: unsupported resolution", ErrProposalInvalid)
	}

	result, err := s.pool.Exec(
		ctx,
		`update public.task_proposals as proposal
		 set status = $4,
		     resolved_at = statement_timestamp()
		 from public.transcript_utterances as source,
		      public.transcript_utterances as current
		 where proposal.user_id = $1::uuid
		   and proposal.session_id = $2::uuid
		   and proposal.status = 'proposed'
		   and source.id = proposal.source_utterance_id
		   and source.user_id = proposal.user_id
		   and source.session_id = proposal.session_id
		   and current.id = $3::uuid
		   and current.user_id = proposal.user_id
		   and current.session_id = proposal.session_id
		   and current.speaker = 'user'
		   and (current.created_at, current.id) > (source.created_at, source.id)
		   and exists (
		       select 1
		       from public.transcript_utterances as prompt
		       where prompt.user_id = proposal.user_id
		         and prompt.session_id = proposal.session_id
		         and prompt.speaker = 'assistant'
		         and (prompt.created_at, prompt.id) > (source.created_at, source.id)
		         and (prompt.created_at, prompt.id) < (current.created_at, current.id)
		   )
		   and not exists (
		       select 1
		       from public.transcript_utterances as intervening
		       where intervening.user_id = proposal.user_id
		         and intervening.session_id = proposal.session_id
		         and intervening.speaker = 'user'
		         and (intervening.created_at, intervening.id) > (source.created_at, source.id)
		         and (intervening.created_at, intervening.id) < (current.created_at, current.id)
		   )`,
		scope.UserID,
		scope.SessionID,
		scope.UtteranceID,
		string(status),
	)

	if err != nil {
		return false, fmt.Errorf("resolve task proposal: %w", err)
	}
	return result.RowsAffected() == 1, nil
}

func validateScope(scope tool.Scope) (tool.Scope, error) {
	scope.UserID = strings.TrimSpace(scope.UserID)
	scope.SessionID = strings.TrimSpace(scope.SessionID)
	if scope.UserID == "" || scope.SessionID == "" {
		return tool.Scope{}, ErrScopeRequired
	}
	scope.UtteranceID = strings.TrimSpace(scope.UtteranceID)
	if scope.UtteranceID == "" {
		return tool.Scope{}, ErrUtteranceRequired
	}
	return scope, nil
}
