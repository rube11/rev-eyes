package reminder

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
		     schedule,
		     due_at
		 )
		 select
		     utterance.user_id,
		     utterance.session_id,
		     utterance.id,
		     'reminder',
		     $4,
		     $5,
		     $6
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
		     due_at = excluded.due_at,
		     created_at = statement_timestamp()
		`,
		scope.UserID,
		scope.SessionID,
		scope.UtteranceID,
		proposal.Title,
		proposal.Schedule,
		proposal.DueAt,
	)
	if err != nil {
		return fmt.Errorf("save task proposal: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrSessionInactive
	}
	return nil
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
