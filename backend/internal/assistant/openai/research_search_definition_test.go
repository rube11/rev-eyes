package openai

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/tool"
	"github.com/rube11/rev-eyes/backend/internal/tool/websearch"
)

func TestSearchDefinitionsPreserveProviderModelSchemaAndStrictMode(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name   string
		config websearch.Config
		modes  []string
	}{
		{"searx_research", websearch.Config{Provider: websearch.ProviderSearXNG, ResearchExtraction: true}, []string{"research"}},
		{"searx_discovery", websearch.Config{Provider: websearch.ProviderSearXNG}, []string{"quick", "research"}},
		{"tavily", websearch.Config{Provider: websearch.ProviderTavily, ResearchExtraction: true, TavilyAPIKey: "offline-schema-fixture"}, []string{"quick", "research"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			searcher, err := websearch.NewConfigured(scenario.config)
			if err != nil {
				t.Fatal(err)
			}
			registry := tool.NewRegistry()
			if err := registry.Register(searcher); err != nil {
				t.Fatal(err)
			}
			definitions, err := toolDefinitions(registry.Specs())
			if err != nil || len(definitions) != 1 {
				t.Fatalf("definitions=%#v error=%v", definitions, err)
			}
			definition := definitions[0]
			if !definition.Strict || definition.Name != "search_web" || string(definition.Parameters) != string(searcher.Spec().Parameters) {
				t.Fatalf("model definition changed provider schema or strict setting: %#v", definition)
			}
			var schema struct {
				Properties map[string]struct {
					Enum []string `json:"enum"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(definition.Parameters, &schema); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(schema.Properties["mode"].Enum, scenario.modes) {
				t.Errorf("model mode enum=%v want=%v", schema.Properties["mode"].Enum, scenario.modes)
			}
		})
	}
}
