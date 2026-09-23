package extraction

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/assistant/jev"
	"github.com/rube11/rev-eyes/backend/internal/memory"
)

// TestLiveJevMemoryClassification verifies the production split: OpenAI writes
// grounded atomic drafts and Jev supplies every fixed metadata label.
func TestLiveJevMemoryClassification(t *testing.T) {
	if os.Getenv("RUN_LIVE_JEV_MEMORY_TEST") != "1" {
		t.Skip("set RUN_LIVE_JEV_MEMORY_TEST=1 to call OpenAI and Jev")
	}
	openAIKey := requiredMemoryLiveEnv(t, "OPENAI_API_KEY")
	model := strings.TrimSpace(os.Getenv("OPENAI_MEMORY_MODEL"))
	if model == "" {
		model = requiredMemoryLiveEnv(t, "OPENAI_ROUTER_MODEL")
	}
	jevClient, err := jev.New(requiredMemoryLiveEnv(t, "JEV_API_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	extractor, err := NewMemoryExtractor(openAIKey, model, jevClient)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	candidates, err := extractor.Extract(
		ctx,
		"I'm working out right now. My daily protein target is 130 grams, and I prefer spicy chicken bowls.",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) < 3 {
		t.Fatalf("candidate count = %d, want at least 3: %#v", len(candidates), candidates)
	}

	var temporary, protein, preference bool
	for _, candidate := range candidates {
		if err := candidate.Validate(); err != nil {
			t.Errorf("invalid candidate %#v: %v", candidate, err)
		}
		switch {
		case candidate.MemoryKey == "state.activity.current":
			temporary = candidate.Retention == memory.RetentionTemporary &&
				candidate.ProfileLayer == memory.ProfileRecent &&
				candidate.Card.Kind == memory.KindEvent
		case candidate.MemoryKey == "profile.nutrition.daily_protein_target":
			protein = candidate.Retention == memory.RetentionDurable &&
				candidate.ProfileLayer == memory.ProfileCore &&
				candidate.Card.Kind == memory.KindGoal
		case strings.Contains(candidate.MemoryKey, "food"):
			preference = candidate.Retention == memory.RetentionDurable &&
				candidate.Card.Kind == memory.KindPreference
		}
		t.Logf("candidate: %+v", candidate)
	}
	if !temporary || !protein || !preference {
		t.Fatalf("classifications: temporary=%v protein=%v preference=%v", temporary, protein, preference)
	}
}

func requiredMemoryLiveEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required", name)
	}
	return value
}
