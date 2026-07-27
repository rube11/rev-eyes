package openai

import (
	"reflect"
	"strings"
	"testing"
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
		"Never choose remember unless the user explicitly asks for it.",
		"a potential task inferred from the speech",
		"future public update over time",
		"Prefer the specialized remember, propose_task, and propose_watch actions over respond",
		`"The meeting starts at three." -> ignore`,
		`"What time does the meeting start?" -> respond`,
		`"I'm walking into the client meeting now." -> state_update`,
		`"Remember that Maya is my manager." -> remember`,
		`"I need to call the dentist tomorrow morning." -> propose_task`,
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
	wantRequired := []string{"action", "query", "memory_lookup", "memory"}
	if got := schema["required"]; !reflect.DeepEqual(got, wantRequired) {
		t.Fatalf("required = %#v, want %#v", got, wantRequired)
	}

	properties := schema["properties"].(map[string]any)
	action := properties["action"].(map[string]any)
	wantActions := []string{
		"ignore",
		"respond",
		"state_update",
		"remember",
		"propose_task",
		"propose_watch",
	}
	if got := action["enum"]; !reflect.DeepEqual(got, wantActions) {
		t.Fatalf("action enum = %#v, want %#v", got, wantActions)
	}
}
