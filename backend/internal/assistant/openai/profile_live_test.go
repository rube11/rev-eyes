package openai

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/assistant"
	"github.com/rube11/rev-eyes/backend/internal/memory"
)

// Opt-in model check: synthetic input only, no database or persistent writes.
func TestProfileExtractionLive(t *testing.T) {
	if os.Getenv("RUN_PROFILE_MODEL_TEST") != "1" {
		t.Skip("set RUN_PROFILE_MODEL_TEST=1")
	}
	model := strings.TrimSpace(os.Getenv("OPENAI_MEMORY_MODEL"))
	if model == "" {
		model = requiredLiveEnv(t, "OPENAI_ROUTER_MODEL")
	}
	for _, tc := range []struct {
		name, text, key string
		layer           memory.ProfileLayer
	}{
		{"core", "my daily protein goal is 150g", "profile.nutrition.daily_protein_target", memory.ProfileCore},
		{"recent", "just left the gym", "state.activity.current", memory.ProfileRecent},
		{"detail", "i like ramen", "profile.food.preference.ramen", memory.ProfileDetail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			extractor, err := NewMemoryExtractor(requiredLiveEnv(t, "OPENAI_API_KEY"), model)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			candidates, err := extractor.Extract(ctx, tc.text)
			if err != nil {
				t.Fatal(err)
			}
			for _, candidate := range candidates {
				if candidate.MemoryKey == tc.key {
					if candidate.ProfileLayer != tc.layer {
						t.Fatalf("layer=%s want=%s", candidate.ProfileLayer, tc.layer)
					}
					t.Logf("%q -> %s", tc.text, candidate.ProfileLayer)
					return
				}
			}
			t.Fatalf("missing expected key %s", tc.key)
		})
	}
}

func TestProfileRoutingLive(t *testing.T) {
	if os.Getenv("RUN_PROFILE_MODEL_TEST") != "1" {
		t.Skip("set RUN_PROFILE_MODEL_TEST=1")
	}
	for _, tc := range []struct {
		text   string
		action assistant.Action
	}{
		{"always keep my protein target in mind", assistant.ActionProfileInclude},
		{"dont include my weight in my profile but keep it saved", assistant.ActionProfileExclude},
	} {
		t.Run(string(tc.action), func(t *testing.T) {
			t.Parallel()
			classify, err := NewClassifier(requiredLiveEnv(t, "OPENAI_API_KEY"), requiredLiveEnv(t, "OPENAI_ROUTER_MODEL"))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			output, err := classify(ctx, tc.text)
			if err != nil {
				t.Fatal(err)
			}
			var decision assistant.Decision
			if err = json.Unmarshal([]byte(output), &decision); err != nil {
				t.Fatal(err)
			}
			if decision.Action != tc.action || decision.Query == "" {
				t.Fatalf("unexpected decision: %#v", decision)
			}
			t.Logf("%q -> %s", tc.text, decision.Action)
		})
	}
}
