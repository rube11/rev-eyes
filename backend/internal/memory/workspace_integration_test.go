package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rube11/rev-eyes/backend/internal/database"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func TestWorkspaceEditByIDPostgres(t *testing.T) {
	if os.Getenv("RUN_DATABASE_INTEGRATION_TEST") != "1" {
		t.Skip("set RUN_DATABASE_INTEGRATION_TEST=1 to run the PostgreSQL integration test")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required when RUN_DATABASE_INTEGRATION_TEST=1")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("pool.Begin() error = %v", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(context.Background()); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			t.Errorf("tx.Rollback() error = %v", rollbackErr)
		}
	}()

	var userID, otherUserID string
	err = tx.QueryRow(ctx, `select id::text from auth.users order by created_at limit 1`).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		t.Skip("database has no auth user for the memory foreign key")
	}
	if err != nil {
		t.Fatalf("select test user: %v", err)
	}
	err = tx.QueryRow(ctx, `select id::text from auth.users where id::text <> $1 order by created_at limit 1`, userID).Scan(&otherUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		otherUserID = "00000000-0000-4000-8000-000000000000"
	} else if err != nil {
		t.Fatalf("select other user: %v", err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var id string
	err = tx.QueryRow(ctx,
		`insert into public.memories (user_id, memory_key, topics, kind, title, summary, profile_layer)
		 values ($1::uuid, $2, array['work']::text[], 'fact', $3, 'Maya leads product at Arcline.', 'detail')
		 returning id::text`,
		userID, "test.workspace.edit."+suffix, "Workspace edit "+suffix,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert test memory: %v", err)
	}
	scope := tool.Scope{UserID: userID}

	type row struct {
		Status, Title, Summary string
		Override               *string
		InactiveAt             *time.Time
		UpdatedAt              time.Time
		SearchHit              bool
	}
	read := func(searchWord string) row {
		t.Helper()
		var r row
		err := tx.QueryRow(ctx,
			`select status, title, summary, profile_override, inactive_at, updated_at,
			        search_document @@ plainto_tsquery('english', $2)
			 from public.memories where id = $1::uuid`, id, searchWord,
		).Scan(&r.Status, &r.Title, &r.Summary, &r.Override, &r.InactiveAt, &r.UpdatedAt, &r.SearchHit)
		if err != nil {
			t.Fatalf("read test memory: %v", err)
		}
		return r
	}
	before := read("Arcline")
	if !before.SearchHit {
		t.Fatal("seed memory is not searchable by its summary")
	}

	// Correct the wording; the generated search document must follow.
	if err := editMemoryByID(ctx, tx, scope, id, WorkspaceEdit{Action: WorkspaceUpdate, Title: "Maya leads product", Summary: "Maya now leads product at Northwind."}); err != nil {
		t.Fatalf("update: %v", err)
	}
	after := read("Northwind")
	if after.Title != "Maya leads product" || after.Summary != "Maya now leads product at Northwind." || !after.SearchHit {
		t.Fatalf("update row = %+v", after)
	}
	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Fatalf("updated_at did not advance: %v -> %v", before.UpdatedAt, after.UpdatedAt)
	}

	// Pin and unpin only touch the override.
	if err := editMemoryByID(ctx, tx, scope, id, WorkspaceEdit{Action: WorkspacePin}); err != nil {
		t.Fatalf("pin: %v", err)
	}
	if r := read(""); r.Override == nil || *r.Override != "core" || r.Status != "active" {
		t.Fatalf("pin row = %+v", r)
	}
	if err := editMemoryByID(ctx, tx, scope, id, WorkspaceEdit{Action: WorkspaceUnpin}); err != nil {
		t.Fatalf("unpin: %v", err)
	}
	if r := read(""); r.Override != nil {
		t.Fatalf("unpin left override %q", *r.Override)
	}

	// Another user cannot touch the row, and neither can a malformed id.
	if err := editMemoryByID(ctx, tx, tool.Scope{UserID: otherUserID}, id, WorkspaceEdit{Action: WorkspaceForget}); !errors.Is(err, ErrMemoryNotFound) {
		t.Fatalf("other user error = %v, want %v", err, ErrMemoryNotFound)
	}
	if err := editMemoryByID(ctx, tx, scope, "not-a-uuid", WorkspaceEdit{Action: WorkspaceForget}); !errors.Is(err, ErrMemoryNotFound) {
		t.Fatalf("malformed id error = %v, want %v", err, ErrMemoryNotFound)
	}

	// Forget, refuse a second forget, then restore.
	if err := editMemoryByID(ctx, tx, scope, id, WorkspaceEdit{Action: WorkspaceForget}); err != nil {
		t.Fatalf("forget: %v", err)
	}
	if r := read(""); r.Status != "forgotten" || r.InactiveAt == nil {
		t.Fatalf("forget row = %+v", r)
	}
	if err := editMemoryByID(ctx, tx, scope, id, WorkspaceEdit{Action: WorkspaceForget}); !errors.Is(err, ErrMemoryNotFound) {
		t.Fatalf("second forget error = %v, want %v", err, ErrMemoryNotFound)
	}
	if err := editMemoryByID(ctx, tx, scope, id, WorkspaceEdit{Action: WorkspaceUpdate, Title: "x", Summary: "y"}); !errors.Is(err, ErrMemoryNotFound) {
		t.Fatalf("update forgotten error = %v, want %v", err, ErrMemoryNotFound)
	}
	if err := editMemoryByID(ctx, tx, scope, id, WorkspaceEdit{Action: WorkspaceRestore}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if r := read(""); r.Status != "active" || r.InactiveAt != nil {
		t.Fatalf("restore row = %+v", r)
	}
	if err := editMemoryByID(ctx, tx, scope, id, WorkspaceEdit{Action: WorkspaceRestore}); !errors.Is(err, ErrMemoryNotFound) {
		t.Fatalf("restore active error = %v, want %v", err, ErrMemoryNotFound)
	}
}
