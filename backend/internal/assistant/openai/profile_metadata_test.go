package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/memory"
)

func TestProfileMetadataMismatchPreservesValidFacts(t *testing.T) {
	output, err := json.Marshal(map[string]any{"memories": []memory.Candidate{
		{MemoryKey: "profile.role.student", Retention: memory.RetentionDurable, ProfileLayer: memory.ProfileCore, Card: memory.Card{Title: "Student", Summary: "The user is a student.", Kind: memory.KindFact, Topics: []memory.Topic{memory.TopicPersonal}, Details: []memory.Detail{}, Entities: []memory.Entity{}}},
		{MemoryKey: "state.activity.current", Retention: memory.RetentionTemporary, ProfileLayer: memory.ProfileCore, Card: memory.Card{Title: "Workout", Summary: "The user just left the gym.", Kind: memory.KindEvent, Topics: []memory.Topic{memory.TopicHealth}, Details: []memory.Detail{}, Entities: []memory.Entity{}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"output": []any{map[string]any{"content": []any{map[string]any{"type": "output_text", "text": string(output)}}}}})
	}))
	defer server.Close()
	extractor, err := NewMemoryExtractor("synthetic-key", "test-model")
	if err != nil {
		t.Fatal(err)
	}
	extractor.endpoint = server.URL
	got, err := extractor.Extract(context.Background(), "I'm a student and I just left the gym.")
	if err != nil || len(got) != 2 {
		t.Fatalf("valid facts discarded with mismatched profile metadata: candidates=%d error=%v", len(got), err)
	}
	if got[0].ProfileLayer != memory.ProfileCore || got[1].ProfileLayer != memory.ProfileRecent || got[1].Retention != memory.RetentionTemporary {
		t.Fatal("normalization changed lifecycle or misplaced facts")
	}
}
