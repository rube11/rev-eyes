package watch

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

var (
	ErrDatabaseRequired  = errors.New("watch database is required")
	ErrScopeRequired     = errors.New("watch scope is required")
	ErrUtteranceRequired = errors.New("watch utterance is required")
	ErrSessionInactive   = errors.New("watch session is not active")
)

// Store persists watch state.
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
	scope.UserID = strings.TrimSpace(scope.UserID)
	scope.SessionID = strings.TrimSpace(scope.SessionID)
	if scope.UserID == "" || scope.SessionID == "" {
		return ErrScopeRequired
	}
	scope.UtteranceID = strings.TrimSpace(scope.UtteranceID)
	if scope.UtteranceID == "" {
		return ErrUtteranceRequired
	}
	proposal = proposal.normalize()
	if err := proposal.validate(); err != nil {
		return err
	}

	result, err := s.pool.Exec(
		ctx,
		`insert into public.watches (
		     user_id,
		     session_id,
		     source_utterance_id,
		     query,
		     condition,
		     interval_minutes,
		     expires_at
		 )
		 select
		     utterance.user_id,
		     utterance.session_id,
		     utterance.id,
		     $4,
		     $5,
		     $6,
		     $7
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
		     query = excluded.query,
		     condition = excluded.condition,
		     interval_minutes = excluded.interval_minutes,
		     expires_at = excluded.expires_at,
		     created_at = statement_timestamp()`,
		scope.UserID,
		scope.SessionID,
		scope.UtteranceID,
		proposal.Query,
		proposal.Condition,
		proposal.IntervalMinutes,
		proposal.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("save watch proposal: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrSessionInactive
	}
	return nil
}
