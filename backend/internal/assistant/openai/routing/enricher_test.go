package routing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/assistant"
	"github.com/rube11/rev-eyes/backend/internal/assistant/openai/responses"
	"github.com/rube11/rev-eyes/backend/internal/session"
)

func TestEnrichmentSchemaCannotChooseAction(t *testing.T) {
	t.Parallel()
	format := enrichmentTextFormat()["format"].(map[string]any)
	schema := format["schema"].(map[string]any)
	properties := schema["properties"].(map[string]any)
	if _, exists := properties["action"]; exists {
		t.Fatalf("enrichment schema can choose an action: %#v", properties)
	}
	wantRequired := []string{"query", "memory_lookup", "memory_review_all"}
	if got := schema["required"]; !reflect.DeepEqual(got, wantRequired) {
		t.Fatalf("required = %#v, want %#v", got, wantRequired)
	}
	if !strings.Contains(enrichmentPrompt, "Do not classify intent") || !strings.Contains(enrichmentPrompt, "action is immutable") {
		t.Fatal("enrichment prompt does not explicitly preserve Jev's action")
	}
}

func TestEnricherReturnsLookupForFixedAction(t *testing.T) {
	t.Parallel()
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": []map[string]any{{
				"type": "message", "content": []map[string]any{{
					"type": "output_text",
					"text": `{"query":"post-workout meal","memory_lookup":{"terms":["protein target"],"topics":["health"],"kinds":["goal"],"entities":[]},"memory_review_all":false}`,
				}},
			}},
		})
	}))
	defer server.Close()

	enricher, err := New("test-key", "test-model", responses.Config{HTTPClient: server.Client(), Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := enricher.Enrich(context.Background(), assistant.ActionRespond, "What should I eat?", session.Conversation{})
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if decision.Action != assistant.ActionRespond {
		t.Fatalf("action = %q, want %q", decision.Action, assistant.ActionRespond)
	}
	if decision.Query != "post-workout meal" || !reflect.DeepEqual(decision.MemoryLookup.Terms, []string{"protein target"}) {
		t.Fatalf("decision = %#v", decision)
	}

	input := received["input"].([]any)
	user := input[0].(map[string]any)["content"].(string)
	var state struct {
		Action assistant.Action `json:"action"`
	}
	if err := json.Unmarshal([]byte(user), &state); err != nil {
		t.Fatalf("decode enrichment input: %v", err)
	}
	if state.Action != assistant.ActionRespond {
		t.Fatalf("input action = %q", state.Action)
	}
}
