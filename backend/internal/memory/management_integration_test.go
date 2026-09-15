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

func TestMemoryManagementPostgres(t *testing.T) {
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

	var userID string
	err = tx.QueryRow(ctx, `select id::text from auth.users order by created_at limit 1`).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		t.Skip("database has no auth user for the memory foreign key")
	}
	if err != nil {
		t.Fatalf("select test user: %v", err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	titles := []string{
		"Jolene pho preference " + suffix,
		"Jolene ramen preference " + suffix,
		"Jolene dim sum preference " + suffix,
	}
	summaries := []string{
		"Jolene likes pho.",
		"Jolene likes ramen.",
		"Jolene likes dim sum.",
	}
	ids := make([]string, len(titles))
	for index := range titles {
		err = tx.QueryRow(
			ctx,
			`insert into public.memories (
			     user_id, memory_key, topics, kind, title, summary, entities
			 ) values (
			     $1::uuid, $2, array['relationships', 'preferences']::text[],
			     'preference', $3, $4,
			     jsonb_build_array(jsonb_build_object('type', 'person', 'name', 'Jolene'))
			 )
			 returning id::text`,
			userID,
			fmt.Sprintf("test.memory.management.%s.%d", suffix, index),
			titles[index],
			summaries[index],
		).Scan(&ids[index])
		if err != nil {
			t.Fatalf("insert test memory %d: %v", index, err)
		}
	}

	reviewed, err := reviewRecent(ctx, tx, userID)
	if err != nil {
		t.Fatalf("reviewRecent() error = %v", err)
	}
	for _, title := range titles {
		if !containsMemoryTitle(reviewed, title) {
			t.Errorf("reviewRecent() did not return %q", title)
		}
	}

	scope := tool.Scope{UserID: userID}
	forgotten, err := forgetWithDatabase(ctx, tx, scope, Lookup{
		Query:    "Jolene likes pho",
		Entities: []string{"Jolene"},
	})
	if err != nil {
		t.Fatalf("forgetWithDatabase(specific) error = %v", err)
	}
	if forgotten != 1 {
		t.Fatalf("forgetWithDatabase(specific) = %d, want 1", forgotten)
	}
	assertMemoryStatuses(t, ctx, tx, ids, []string{"forgotten", "active", "active"})

	forgotten, err = forgetWithDatabase(ctx, tx, scope, Lookup{Entities: []string{"Jolene"}})
	if !errors.Is(err, ErrMemoryAmbiguous) {
		t.Fatalf("forgetWithDatabase(ambiguous) error = %v, want %v", err, ErrMemoryAmbiguous)
	}
	if forgotten != 0 {
		t.Fatalf("forgetWithDatabase(ambiguous) = %d, want 0", forgotten)
	}
	assertMemoryStatuses(t, ctx, tx, ids, []string{"forgotten", "active", "active"})
}

func containsMemoryTitle(cards []Card, title string) bool {
	for _, card := range cards {
		if card.Title == title {
			return true
		}
	}
	return false
}

func assertMemoryStatuses(
	t *testing.T,
	ctx context.Context,
	tx pgx.Tx,
	ids []string,
	want []string,
) {
	t.Helper()
	for index, id := range ids {
		var status string
		if err := tx.QueryRow(ctx, `select status from public.memories where id = $1::uuid`, id).Scan(&status); err != nil {
			t.Fatalf("read test memory %d status: %v", index, err)
		}
		if status != want[index] {
			t.Errorf("test memory %d status = %q, want %q", index, status, want[index])
		}
	}
}
