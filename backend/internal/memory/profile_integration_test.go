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

// This test uses fake owners and facts and always rolls back, including DDL
// when exercising the migration before deployment. No model calls are made.
func TestProfilePostgres(t *testing.T) {
	if os.Getenv("RUN_PROFILE_DATABASE_TEST") != "1" {
		t.Skip("set RUN_PROFILE_DATABASE_TEST=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Error(err)
		}
	}()
	if _, err = tx.Exec(ctx, "set local lock_timeout = '3s'"); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err = tx.QueryRow(ctx, `select exists(select 1 from information_schema.columns where table_schema='public' and table_name='memories' and column_name='profile_layer')`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		migration, err := os.ReadFile("../../migrations/0018_memory_profile.sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	var userID, otherID, sessionID, sourceID string
	for _, target := range []*string{&userID, &otherID} {
		if err = tx.QueryRow(ctx, `insert into auth.users(id) values(gen_random_uuid()) returning id::text`).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.QueryRow(ctx, `insert into public.sessions(user_id) values($1::uuid) returning id::text`, userID).Scan(&sessionID); err != nil {
		t.Fatal(err)
	}
	observed := time.Now().UTC()
	if err = tx.QueryRow(ctx, `insert into public.transcript_utterances(user_id,session_id,speaker,text,started_at,ended_at)
	    values($1::uuid,$2::uuid,'user','Synthetic profile test source',$3,$3) returning id::text`, userID, sessionID, observed).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	scope := tool.Scope{UserID: userID}
	insert := func(key, summary, layer, status string, expired bool) {
		t.Helper()
		_, err := tx.Exec(ctx, `insert into public.memories(user_id,memory_key,topics,kind,title,summary,profile_layer,status,inactive_at,created_at,expires_at)
		    values($1::uuid,$2,array['personal'],'fact',$3,$3,$4,$5,
		        case when $5='forgotten' then now() else null end,now()-interval '2 hours',
		        case when $6 then now()-interval '1 hour' when $4='recent' then now()+interval '1 hour' else null end)`, userID, key, summary, layer, status, expired)
		if err != nil {
			t.Fatal(err)
		}
	}
	insert("profile.role.student", "Student at North College.", "core", "active", false)
	insert("state.activity.current", "Just left the gym.", "recent", "active", false)
	insert("test.expired", "Old workout context.", "recent", "active", true)
	insert("test.forgotten", "Forgotten fact.", "core", "forgotten", false)
	insert("test.detail", "Likes blue plates.", "detail", "active", false)
	if _, err = tx.Exec(ctx, `insert into public.memories(user_id,topics,kind,title,summary,profile_layer) values($1::uuid,array['personal'],'fact','Other owner','Other owner secret.','core')`, otherID); err != nil {
		t.Fatal(err)
	}
	check := func(want, unwanted []string) {
		t.Helper()
		profile, err := profileWithDatabase(ctx, tx, scope)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range want {
			if !strings.Contains(profile, s) {
				t.Errorf("missing synthetic profile fact %q", s)
			}
		}
		for _, s := range unwanted {
			if strings.Contains(profile, s) {
				t.Errorf("unexpected synthetic profile fact %q", s)
			}
		}
	}
	check([]string{"Student at North College", "Just left the gym"}, []string{"Old workout", "Forgotten fact", "blue plates", "Other owner"})
	// Exclusion preserves the searchable card, including after correction.
	lookup := Lookup{Query: "North College"}
	if count, err := editMemoryWithDatabase(ctx, tx, scope, lookup, "detail"); err != nil || count != 1 {
		t.Fatalf("exclude: count=%d err=%v", count, err)
	}
	check(nil, []string{"North College"})
	var active int
	if err = tx.QueryRow(ctx, `select count(*) from public.memories where user_id=$1::uuid and memory_key='profile.role.student' and status='active'`, userID).Scan(&active); err != nil || active != 1 {
		t.Fatal("exclusion deleted memory", err)
	}
	candidate := Candidate{MemoryKey: "profile.role.student", Retention: RetentionDurable, ProfileLayer: ProfileCore, Card: Card{Topics: []Topic{TopicPersonal}, Kind: KindFact, Title: "College", Summary: "Student at South College."}}.Normalize()
	encoded, err := encodeCandidate(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if accepted, err := upsertCandidate(ctx, tx, userID, sourceID, observed.Add(time.Second), encoded); err != nil || !accepted {
		t.Fatal("correction failed", err)
	}
	check(nil, []string{"North College", "South College"})
	lookup = Lookup{Query: "South College"}
	if count, err := editMemoryWithDatabase(ctx, tx, scope, lookup, "core"); err != nil || count != 1 {
		t.Fatal("include failed", err)
	}
	check([]string{"South College"}, []string{"North College"})
	if accepted, err := upsertCandidate(ctx, tx, userID, sourceID, observed, encoded); err != nil || accepted {
		t.Fatal("older source should not overwrite", err)
	}
	// Ambiguous or cross-owner edits must not change anything.
	insert("test.college", "Another College fact.", "core", "active", false)
	if _, err := editMemoryWithDatabase(ctx, tx, scope, Lookup{Query: "College"}, "detail"); !errors.Is(err, ErrMemoryAmbiguous) {
		t.Fatal("ambiguous edit accepted", err)
	}
	if count, err := editMemoryWithDatabase(ctx, tx, tool.Scope{UserID: otherID}, lookup, "detail"); err != nil || count != 0 {
		t.Fatal("cross-owner edit", err)
	}
	if count, err := forgetWithDatabase(ctx, tx, scope, lookup); err != nil || count != 1 {
		t.Fatal("forget failed", err)
	}
	check(nil, []string{"South College"})
	// More duplicate rows than the SQL limit must not hide a distinct fact.
	for index := 0; index < profileSectionLimit+1; index++ {
		summary := "Same repeated profile fact."
		if index%2 == 0 {
			summary = "  Same\t repeated  profile fact.  "
		}
		insert(fmt.Sprintf("test.duplicate.%d", index), summary, "core", "active", false)
	}
	insert("test.unique", "Avoids peanuts.", "core", "active", false)
	if _, err = tx.Exec(ctx, `update public.memories set observed_at = now() + interval '1 minute'
	    where user_id=$1::uuid and memory_key like 'test.duplicate.%'`, userID); err != nil {
		t.Fatal(err)
	}
	var pinnedID string
	if err = tx.QueryRow(ctx, `update public.memories set profile_override='core', observed_at=now()-interval '1 day'
	    where user_id=$1::uuid and memory_key='test.duplicate.0' returning id::text`, userID).Scan(&pinnedID); err != nil {
		t.Fatal(err)
	}
	profile, err := profileWithDatabase(ctx, tx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(profile, "Avoids peanuts.") || strings.Count(profile, "repeated profile fact.") != 1 || !strings.Contains(profile, "[memory:"+pinnedID+"]") {
		t.Fatal("deduplication lost a distinct fact, repeated a fact, or discarded the pinned source")
	}
	if _, err := profileWithDatabase(ctx, tx, tool.Scope{}); !errors.Is(err, ErrScopeRequired) {
		t.Fatal("missing scope accepted", err)
	}
}
