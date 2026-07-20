package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

var (
	ErrDatabaseRequired  = errors.New("memory database is required")
	ErrScopeRequired     = errors.New("memory scope is required")
	ErrSourceRequired    = errors.New("source utterance ID is required")
	ErrTextRequired      = errors.New("memory text is required")
	ErrSourceUnavailable = errors.New("source utterance is unavailable")
)

// Store persists confirmed memories and their transcript sources.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, ErrDatabaseRequired
	}
	return &Store{pool: pool}, nil
}

// Remember saves one confirmed memory linked to its trusted source utterance.
func (s *Store) Remember(
	ctx context.Context,
	scope tool.Scope,
	sourceUtteranceID string,
	text string,
) error {
	scope.UserID = strings.TrimSpace(scope.UserID)
	scope.SessionID = strings.TrimSpace(scope.SessionID)
	if scope.UserID == "" || scope.SessionID == "" {
		return ErrScopeRequired
	}
	sourceUtteranceID = strings.TrimSpace(sourceUtteranceID)
	if sourceUtteranceID == "" {
		return ErrSourceRequired
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ErrTextRequired
	}

	var memoryID string
	err := s.pool.QueryRow(
		ctx,
		`with source as (
		     select id, user_id
		     from public.transcript_utterances
		     where id = $3::uuid
		       and user_id = $1::uuid
		       and session_id = $2::uuid
		 ),
		 created_memory as (
		     insert into public.memories (user_id, text)
		     select user_id, $4
		     from source
		     returning id, user_id
		 )
		 insert into public.memory_sources (
		     user_id,
		     memory_id,
		     utterance_id
		 )
		 select
		     created_memory.user_id,
		     created_memory.id,
		     source.id
		 from created_memory
		 join source using (user_id)
		 returning memory_id::text`,
		scope.UserID,
		scope.SessionID,
		sourceUtteranceID,
		text,
	).Scan(&memoryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSourceUnavailable
	}
	if err != nil {
		return fmt.Errorf("save confirmed memory: %w", err)
	}
	return nil
}
