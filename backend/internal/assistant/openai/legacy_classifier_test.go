package openai

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/assistant"
)

func TestClassifierRequestPreservesRoutingContract(t *testing.T) {
	t.Parallel()

	const utterance = "What time does the meeting start?"
	request := classifierRequest("test-model", utterance)

	if got := request["model"]; got != "test-model" {
		t.Fatalf("model = %#v, want %q", got, "test-model")
	}
	if got := request["store"]; got != false {
		t.Fatalf("store = %#v, want false", got)
	}

	input, ok := request["input"].([]map[string]string)
	if !ok || len(input) != 2 {
		t.Fatalf("input = %#v, want two messages", request["input"])
	}
	if got := input[0]; got["role"] != "system" || got["content"] != routerPrompt {
		t.Fatalf("system input = %#v", got)
	}
	if got := input[1]; got["role"] != "user" || got["content"] != utterance {
		t.Fatalf("user input = %#v", got)
	}

	wantPromptRules := []string{
		"ordinary factual statement that does not ask the assistant for anything",
		"direct question addressed to the assistant or a direct command",
		"never respond to an ordinary statement just to volunteer information",
		"This action is silent: it must not wake the assistant or produce a visible response.",
		"a bare statement—not a question or command",
		"Starts, arrivals, and ordinary location changes belong here.",
		"Never choose remember unless the user explicitly asks for it.",
		"request to inspect what the assistant remembers",
		"user says a remembered detail is wrong or supplies a replacement value",
		"explicit request to remove a remembered detail",
		"an explicit reminder request or a potential task inferred from the speech",
		"future public update over time",
		"Direct questions and commands use respond even when they mention a transition.",
		"Otherwise prefer the specialized memory, transition, task, and watch actions when their definitions apply.",
		`"The meeting starts at three." -> ignore`,
		`"What time does the meeting start?" -> respond`,
		`"I'm walking into the client meeting now." -> state_update`,
		`"I just arrived at the gym." -> state_update`,
		`"I just left the gym." -> state_transition`,
		`"I just left the gym; what should I eat?" -> respond`,
		`"I finished my exam." -> state_transition`,
		`"Remember that Maya is my manager." -> remember`,
		`"What do you remember about Jolene?" -> memory_review`,
		`"That's wrong." -> memory_correct with an empty query`,
		`"Change my protein target to 150 grams." -> memory_correct`,
		`"Forget that." -> memory_forget with an empty query`,
		"search for context that can decide the next move rather than merely repeating the transition",
		"finishing an exam or study session: pending commitments, deadlines, instructions, and next priorities",
		`"I need to call the dentist tomorrow morning." -> propose_task`,
		`"Remind me to call the dentist tomorrow at nine." -> propose_task`,
		`"Keep me updated when the election result is announced." -> propose_watch`,
	}
	for _, rule := range wantPromptRules {
		if !strings.Contains(routerPrompt, rule) {
			t.Errorf("routerPrompt missing routing contract %q", rule)
		}
	}
}

func TestClassifierRequestUsesStrictCompleteActionSchema(t *testing.T) {
	t.Parallel()

	request := classifierRequest("test-model", "hello")
	textFormat := request["text"].(map[string]any)["format"].(map[string]any)
	if textFormat["type"] != "json_schema" || textFormat["strict"] != true {
		t.Fatalf("text format = %#v", textFormat)
	}

	schema := textFormat["schema"].(map[string]any)
	if schema["additionalProperties"] != false {
		t.Fatalf("additionalProperties = %#v, want false", schema["additionalProperties"])
	}
	wantRequired := []string{"action", "query", "memory_lookup", "memory_review_all"}
	if got := schema["required"]; !reflect.DeepEqual(got, wantRequired) {
		t.Fatalf("required = %#v, want %#v", got, wantRequired)
	}

	properties := schema["properties"].(map[string]any)
	if _, exists := properties["memory"]; exists {
		t.Fatalf("router schema still contains deprecated memory payload: %#v", properties["memory"])
	}
	action := properties["action"].(map[string]any)
	wantActions := []string{
		"ignore",
		"respond",
		"state_update",
		"state_transition",
		"remember",
		"memory_review",
		"memory_correct",
		"memory_forget",
		"profile_include",
		"profile_exclude",
		"propose_task",
		"propose_watch",
	}
	if got := action["enum"]; !reflect.DeepEqual(got, wantActions) {
		t.Fatalf("action enum = %#v, want %#v", got, wantActions)
	}
	memoryLookup := properties["memory_lookup"].(map[string]any)
	lookupProperties := memoryLookup["properties"].(map[string]any)
	if got := lookupProperties["terms"].(map[string]any)["maxItems"]; got != 5 {
		t.Fatalf("memory terms maxItems = %#v, want 5", got)
	}
	if got := lookupProperties["topics"].(map[string]any)["maxItems"]; got != 3 {
		t.Fatalf("memory topics maxItems = %#v, want 3", got)
	}
}

func TestLiveMemoryManagementRouting(t *testing.T) {
	if os.Getenv("RUN_LIVE_ASSISTANT_TEST") != "1" {
		t.Skip("set RUN_LIVE_ASSISTANT_TEST=1 to call the live OpenAI API")
	}
	classify, err := NewClassifier(
		requiredLiveEnv(t, "OPENAI_API_KEY"),
		requiredLiveEnv(t, "OPENAI_ROUTER_MODEL"),
	)
	if err != nil {
		t.Fatalf("NewClassifier() error = %v", err)
	}
	router := assistant.NewRouter(classify)
	tests := []struct {
		name       string
		utterance  string
		wantAction assistant.Action
		wantQuery  bool
		wantEntity string
		wantLookup bool
	}{
		{"review Jolene", "What do you remember about Jolene?", assistant.ActionMemoryReview, true, "jolene", true},
		{"incomplete correction", "That's wrong.", assistant.ActionMemoryCorrect, false, "", false},
		{"contextual forget", "Forget that.", assistant.ActionMemoryForget, false, "", false},
		{"replace protein target", "Change my protein target to 150 grams.", assistant.ActionMemoryCorrect, true, "", false},
		{"gym arrival stays silent", "I just arrived at the gym.", assistant.ActionStateUpdate, true, "", false},
		{"gym transition", "I just left the gym.", assistant.ActionStateTransition, true, "", true},
		{"gym transition question responds", "I just left the gym; what should I eat?", assistant.ActionRespond, true, "", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			decision, err := router.Route(ctx, test.utterance)
			if err != nil {
				t.Fatalf("Route() error = %v", err)
			}
			if decision.Action != test.wantAction {
				t.Fatalf("action = %q, want %q; decision = %#v", decision.Action, test.wantAction, decision)
			}
			if (strings.TrimSpace(decision.Query) != "") != test.wantQuery {
				t.Errorf("query = %q, want nonempty = %v", decision.Query, test.wantQuery)
			}
			if test.wantLookup && decision.MemoryLookup.Empty() &&
				len(decision.MemoryLookup.Topics) == 0 && len(decision.MemoryLookup.Kinds) == 0 {
				t.Errorf("lookup is empty: %#v", decision.MemoryLookup)
			}
			if test.wantEntity != "" && !containsFold(decision.MemoryLookup.Entities, test.wantEntity) {
				t.Errorf("entities = %q, want %q", decision.MemoryLookup.Entities, test.wantEntity)
			}
			t.Logf("decision: %#v", decision)
		})
	}
}

func containsFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(value, want) {
			return true
		}
	}
	return false
}
