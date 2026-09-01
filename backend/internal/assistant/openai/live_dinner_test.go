package openai

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/assistant"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

// TestLiveSyntheticMemoryScenarios shows the live router lookup and live agent
// response for every synthetic scenario. The cards are deliberately injected:
// this evaluates lookup generation and context use without touching a real
// user's database or pretending to evaluate PostgreSQL retrieval ranking.
func TestLiveSyntheticMemoryScenarios(t *testing.T) {
	if os.Getenv("RUN_LIVE_ASSISTANT_TEST") != "1" {
		t.Skip("set RUN_LIVE_ASSISTANT_TEST=1 to call the live OpenAI API")
	}

	apiKey := requiredLiveEnv(t, "OPENAI_API_KEY")
	classify, err := NewClassifier(apiKey, requiredLiveEnv(t, "OPENAI_ROUTER_MODEL"))
	if err != nil {
		t.Fatalf("NewClassifier() error = %v", err)
	}

	registry := tool.NewRegistry()
	executor, err := tool.NewExecutor(registry)
	if err != nil {
		t.Fatalf("tool.NewExecutor() error = %v", err)
	}
	agent, err := NewAgent(
		apiKey,
		requiredLiveEnv(t, "OPENAI_AGENT_MODEL"),
		registry,
		executor,
	)
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}

	router := assistant.NewRouter(classify)
	scope := tool.Scope{
		UserID:    "00000000-0000-0000-0000-000000000001",
		SessionID: "00000000-0000-0000-0000-000000000002",
		TimeZone:  "America/Los_Angeles",
	}

	for _, scenario := range syntheticMemoryScenarios() {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()

			decision, err := router.Route(ctx, scenario.spoken)
			if err != nil {
				t.Fatalf("Route() error = %v", err)
			}
			encodedDecision, err := json.MarshalIndent(decision, "", "  ")
			if err != nil {
				t.Fatalf("encode router decision: %v", err)
			}
			t.Logf("spoken:\n%s", scenario.spoken)
			t.Logf("live router decision and lookup:\n%s", encodedDecision)
			if decision.Action != assistant.ActionRespond {
				t.Errorf("router action = %q, want %q", decision.Action, assistant.ActionRespond)
				return
			}
			query := strings.TrimSpace(decision.Query)
			if query == "" {
				query = scenario.spoken
			}

			result, err := agent.RespondWithResult(
				ctx,
				scope,
				query,
				session.Conversation{},
				scenario.memories,
			)
			if err != nil {
				t.Fatalf("RespondWithResult() error = %v", err)
			}
			if strings.TrimSpace(result.Text) == "" {
				t.Fatal("assistant response is empty")
			}

			encodedMemories, err := json.MarshalIndent(scenario.memories, "", "  ")
			if err != nil {
				t.Fatalf("encode fake memories: %v", err)
			}
			t.Logf("injected fake context (%d memories):\n%s", len(scenario.memories), encodedMemories)
			t.Logf("live assistant response:\n%s", result.Text)
			t.Logf("glasses preview:\n%s", glassesPreview(result.Text))
		})
	}
}

func requiredLiveEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required for the live integration test", name)
	}
	return value
}

func glassesPreview(response string) string {
	const maxCharacters = 340
	characters := []rune(strings.TrimSpace(response))
	if len(characters) <= maxCharacters {
		return string(characters)
	}
	return string(characters[:maxCharacters-1]) + "…"
}
