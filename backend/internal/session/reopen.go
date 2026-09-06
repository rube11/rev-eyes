package session

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

// Reopen atomically selects an owned chat as the user's single active session.
// History and summaries remain attached to their original chats.
func (s *Store) Reopen(ctx context.Context, scope tool.Scope) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin session switch: %w", err)
	}
	defer tx.Rollback(context.Background())
	if err := reopenInTransaction(ctx, tx, scope); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit session switch: %w", err)
	}
	return nil
}

func reopenInTransaction(ctx context.Context, tx pgx.Tx, scope tool.Scope) error {
	scope.UserID = strings.TrimSpace(scope.UserID)
	scope.SessionID = strings.TrimSpace(scope.SessionID)
	if scope.UserID == "" || scope.SessionID == "" {
		return ErrScopeRequired
	}
	// Use the same account lock as Resume, including connections obtaining tickets.
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1, 0::bigint))`, scope.UserID); err != nil {
		return fmt.Errorf("lock session switch: %w", err)
	}
	var found int
	err := tx.QueryRow(ctx, `select 1 from public.sessions where id = $2::uuid and user_id = $1::uuid for update`, scope.UserID, scope.SessionID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSessionUnavailable
	}
	if err != nil {
		return fmt.Errorf("find owned session: %w", err)
	}
	// Validate ownership before changing the currently active session.
	if _, err := tx.Exec(ctx, `update public.sessions set status = 'ended', ended_at = statement_timestamp()
 where user_id = $1::uuid and status = 'active' and id <> $2::uuid`, scope.UserID, scope.SessionID); err != nil {
		return fmt.Errorf("end previous session: %w", err)
	}
	if _, err := tx.Exec(ctx, `update public.sessions
 set status = 'active', ended_at = null, last_activity_at = statement_timestamp()
 where id = $2::uuid and user_id = $1::uuid`, scope.UserID, scope.SessionID); err != nil {
		return fmt.Errorf("reopen conversation: %w", err)
	}
	return nil
}
