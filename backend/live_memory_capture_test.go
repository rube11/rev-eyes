package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	assistantopenai "github.com/rube11/rev-eyes/backend/internal/assistant/openai"
	"github.com/rube11/rev-eyes/backend/internal/memory"
)

const liveProfileStatement = "Hey, I am working out. I typically tend to need to eat around 2,000 to 3,000 calories and 130 grams of protein a day. I also love flavorful meals with steak, chicken, pasta, and rice. I am around 150 pounds, and I am also a student."

// TestLiveMemoryCapture is an opt-in evaluation of the independent memory
// learner. It never writes to the database or a real user's memory store.
func TestLiveMemoryCapture(t *testing.T) {
	if os.Getenv("RUN_LIVE_MEMORY_EVAL") != "1" {
		t.Skip("set RUN_LIVE_MEMORY_EVAL=1 to call the live memory model")
	}

	model := strings.TrimSpace(os.Getenv("OPENAI_MEMORY_MODEL"))
	if model == "" {
		model = requiredMemoryLiveEnv(t, "OPENAI_ROUTER_MODEL")
	}
	extractor, err := assistantopenai.NewMemoryExtractor(
		requiredMemoryLiveEnv(t, "OPENAI_API_KEY"),
		model,
	)
	if err != nil {
		t.Fatalf("NewMemoryExtractor() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	candidates, err := extractor.Extract(ctx, liveProfileStatement)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	encoded, err := json.MarshalIndent(candidates, "", "  ")
	if err != nil {
		t.Fatalf("encode candidates: %v", err)
	}
	t.Logf("utterance:\n%s", liveProfileStatement)
	t.Logf("atomic memories (%d):\n%s", len(candidates), encoded)

	if len(candidates) < 5 {
		t.Fatalf("captured %d memories, want at least 5", len(candidates))
	}
	keys := make(map[string]struct{}, len(candidates))
	hasProtein := false
	hasStudent := false
	hasTemporaryState := false
	for _, candidate := range candidates {
		if err := candidate.Validate(); err != nil {
			t.Fatalf("candidate %q is invalid: %v", candidate.MemoryKey, err)
		}
		if _, duplicate := keys[candidate.MemoryKey]; duplicate {
			t.Fatalf("duplicate memory key %q", candidate.MemoryKey)
		}
		keys[candidate.MemoryKey] = struct{}{}
		searchable := strings.ToLower(candidate.MemoryKey + " " + candidate.Card.Summary)
		hasProtein = hasProtein || strings.Contains(searchable, "protein")
		hasStudent = hasStudent || strings.Contains(searchable, "student")
		hasTemporaryState = hasTemporaryState || candidate.Retention == memory.RetentionTemporary
	}
	if !hasProtein || !hasStudent || !hasTemporaryState {
		t.Fatalf(
			"missing required structure: protein=%t student=%t temporary_state=%t",
			hasProtein,
			hasStudent,
			hasTemporaryState,
		)
	}
}

func requiredMemoryLiveEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required for the live integration test", name)
	}
	return value
}
