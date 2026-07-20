package session

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const resumeWindow = "30 minutes"

var (
	ErrDatabaseRequired   = errors.New("session database is required")
	ErrScopeRequired      = errors.New("session scope is required")
	ErrSpeakerInvalid     = errors.New("transcript speaker is invalid")
	ErrTextRequired       = errors.New("transcript text is required")
	ErrSessionUnavailable = errors.New("session is not active")
)

type Speaker string

const (
	SpeakerUser      Speaker = "user"
	SpeakerAssistant Speaker = "assistant"
)

// Store persists application sessions and their finalized transcript.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, ErrDatabaseRequired
	}
	return &Store{pool: pool}, nil
}

// Resume returns the user's recent active session or creates a new one.
func (s *Store) Resume(ctx context.Context, userID string) (string, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "", ErrScopeRequired
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin session transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(
		ctx,
		`select pg_advisory_xact_lock(hashtextextended($1, 0::bigint))`,
		userID,
	); err != nil {
		return "", fmt.Errorf("lock user session: %w", err)
	}

	if _, err := tx.Exec(
		ctx,
		`update public.sessions
		 set status = 'expired', ended_at = now()
		 where user_id = $1::uuid
		   and status = 'active'
		   and last_activity_at < now() - $2::interval`,
		userID,
		resumeWindow,
	); err != nil {
		return "", fmt.Errorf("expire inactive sessions: %w", err)
	}

	var sessionID string
	err = tx.QueryRow(
		ctx,
		`select id::text
		 from public.sessions
		 where user_id = $1::uuid
		   and status = 'active'
		 order by last_activity_at desc
		 limit 1`,
		userID,
	).Scan(&sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(
			ctx,
			`insert into public.sessions (user_id)
			 values ($1::uuid)
			 returning id::text`,
			userID,
		).Scan(&sessionID)
	}
	if err != nil {
		return "", fmt.Errorf("resolve user session: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit session transaction: %w", err)
	}
	return sessionID, nil
}

// Append records one finalized utterance and refreshes session activity.
func (s *Store) Append(
	ctx context.Context,
	scope tool.Scope,
	speaker Speaker,
	text string,
) (string, error) {
	scope.UserID = strings.TrimSpace(scope.UserID)
	scope.SessionID = strings.TrimSpace(scope.SessionID)
	if scope.UserID == "" || scope.SessionID == "" {
		return "", ErrScopeRequired
	}
	if speaker != SpeakerUser && speaker != SpeakerAssistant {
		return "", ErrSpeakerInvalid
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ErrTextRequired
	}

	var utteranceID string
	err := s.pool.QueryRow(
		ctx,
		`with active_session as (
		     update public.sessions
		     set last_activity_at = statement_timestamp()
		     where id = $2::uuid
		       and user_id = $1::uuid
		       and status = 'active'
		     returning id
		 )
		 insert into public.transcript_utterances (
		     user_id,
		     session_id,
		     speaker,
		     text,
		     started_at,
		     ended_at
		 )
		 select
		     $1::uuid,
		     id,
		     $3,
		     $4,
		     statement_timestamp(),
		     statement_timestamp()
		 from active_session
		 returning id::text`,
		scope.UserID,
		scope.SessionID,
		string(speaker),
		text,
	).Scan(&utteranceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrSessionUnavailable
	}
	if err != nil {
		return "", fmt.Errorf("append transcript utterance: %w", err)
	}
	return utteranceID, nil
}
