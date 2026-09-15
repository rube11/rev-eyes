package session

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rube11/rev-eyes/backend/internal/database"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

// All fixtures and session changes live inside one rolled-back transaction.
func TestReopenPostgres(t *testing.T) {
	if os.Getenv("RUN_DATABASE_INTEGRATION_TEST") != "1" {
		t.Skip("set RUN_DATABASE_INTEGRATION_TEST=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal("database connection failed")
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var owner string
	if err := tx.QueryRow(ctx, `select id::text from auth.users order by created_at limit 1`).Scan(&owner); err != nil {
		t.Fatal("test requires an existing owner")
	}
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1, 0::bigint))`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `update public.sessions set status='ended', ended_at=statement_timestamp() where user_id=$1::uuid and status='active'`, owner); err != nil {
		t.Fatal(err)
	}
	var current, old string
	if err := tx.QueryRow(ctx, `insert into public.sessions(user_id) values($1::uuid) returning id::text`, owner).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `insert into public.sessions(user_id,status,ended_at) values($1::uuid,'expired',now()) returning id::text`, owner).Scan(&old); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `insert into public.transcript_utterances(user_id,session_id,speaker,text,started_at,ended_at) values($1::uuid,$2::uuid,'user','Fixture message',now(),now())`, owner, old); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `update public.sessions set context_summary='Keep this context', context_summary_through_id=(select id from public.transcript_utterances where session_id=$1::uuid limit 1) where id=$1::uuid`, old); err != nil {
		t.Fatal(err)
	}

	// Prove this database reproduces the original bug, rather than mocking it.
	savepoint, err := tx.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = savepoint.Exec(ctx, `update public.sessions set status='active',ended_at=null where id=$1::uuid`, old)
	var constraintErr *pgconn.PgError
	if !errors.As(err, &constraintErr) || constraintErr.Code != "23505" || constraintErr.ConstraintName != "sessions_one_active_per_user_idx" {
		t.Fatalf("expected original unique-index conflict, got %v", err)
	}
	if err := savepoint.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	assertActive := func(id string) {
		t.Helper()
		var count int
		var active string
		if err := tx.QueryRow(ctx, `select count(*),min(id::text) from public.sessions where user_id=$1::uuid and status='active'`, owner).Scan(&count, &active); err != nil {
			t.Fatal(err)
		}
		if count != 1 || active != id {
			t.Fatal("expected exactly the selected active session")
		}
	}
	scope := tool.Scope{UserID: owner, SessionID: old}
	if err := reopenInTransaction(ctx, tx, scope); err != nil {
		t.Fatal(err)
	}
	assertActive(old)
	if err := reopenInTransaction(ctx, tx, scope); err != nil {
		t.Fatal(err)
	}
	assertActive(old)
	// A retained glasses socket can select its original chat on its next turn.
	if err := reopenInTransaction(ctx, tx, tool.Scope{UserID: owner, SessionID: current}); err != nil {
		t.Fatal(err)
	}
	assertActive(current)
	// Invalid ownership must not close the account's current chat.
	err = reopenInTransaction(ctx, tx, tool.Scope{UserID: owner, SessionID: "00000000-0000-0000-0000-000000000000"})
	if !errors.Is(err, ErrSessionUnavailable) {
		t.Fatal("expected missing/foreign session rejection")
	}
	assertActive(current)
	err = reopenInTransaction(ctx, tx, tool.Scope{UserID: "00000000-0000-0000-0000-000000000000", SessionID: old})
	if !errors.Is(err, ErrSessionUnavailable) {
		t.Fatal("expected foreign-owner rejection")
	}
	assertActive(current)
	var summary string
	var messages int
	if err := tx.QueryRow(ctx, `select context_summary,(select count(*) from public.transcript_utterances where session_id=$1::uuid) from public.sessions where id=$1::uuid`, old).Scan(&summary, &messages); err != nil {
		t.Fatal(err)
	}
	if summary != "Keep this context" || messages != 1 {
		t.Fatal("session switching changed history")
	}
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		t.Fatal(err)
	}
	var fixtures int
	if err := pool.QueryRow(ctx, `select count(*) from public.sessions where id in ($1::uuid,$2::uuid)`, current, old).Scan(&fixtures); err != nil {
		t.Fatal(err)
	}
	if fixtures != 0 {
		t.Fatal("test fixtures were not rolled back")
	}
}
