package websearch

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestSearchSchemaDescriptionsUseTargetedQueriesAndEvidenceBasedDomains(t *testing.T) {
	t.Parallel()
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(parametersSchema), &schema); err != nil {
		t.Fatal(err)
	}
	query := strings.ToLower(schema.Properties["query"].Description)
	for _, required := range []string{"concise search keywords", "budgets", "preferences", "companion names", "named candidate", "missing fact", "include_domains"} {
		if !strings.Contains(query, required) {
			t.Errorf("query description lost targeted-search guidance %q", required)
		}
	}
	for _, obsolete := range []string{"natural-language question", "do not use a keyword list"} {
		if strings.Contains(query, obsolete) {
			t.Errorf("query description contradicts concise keyword planning: %q", obsolete)
		}
	}
	mode := strings.ToLower(schema.Properties["mode"].Description)
	for _, required := range []string{"quick only to discover", "does not fetch source pages", "research", "every follow-up", "prices", "hours", "schedules"} {
		if !strings.Contains(mode, required) {
			t.Errorf("mode description lost fetched-evidence requirement %q", required)
		}
	}
	domains := strings.ToLower(schema.Properties["include_domains"].Description)
	for _, required := range []string{"bare hostnames", "empty array", "operator is unknown", "supplied by the user", "identified by retrieved evidence", "never guess", "without a domain filter", "do not repeat the same failed restriction"} {
		if !strings.Contains(domains, required) {
			t.Errorf("domain description lost source-discovery safeguard %q", required)
		}
	}
	for _, biasedExample := range []string{"nps.gov", "blm.gov", "recreation.gov"} {
		if strings.Contains(domains, biasedExample) {
			t.Errorf("domain description must not prime a scenario-specific operator: %q", biasedExample)
		}
	}
}

func TestSearchSchemaDescriptionChangePreservesFieldContract(t *testing.T) {
	t.Parallel()
	// Remove descriptions and compare the complete remaining schema. This
	// protects names, types, required fields, enums, and domain-count bounds.
	const expected = `{
		"type":"object",
		"properties":{
			"query":{"type":"string"},
			"mode":{"type":"string","enum":["quick","research"]},
			"topic":{"type":"string","enum":["general","news"]},
			"recency":{"type":"string","enum":["none","day","week","month","year"]},
			"include_domains":{"type":"array","items":{"type":"string"},"maxItems":5}
		},
		"required":["query","mode","topic","recency","include_domains"],
		"additionalProperties":false
	}`
	var got, want map[string]any
	if err := json.Unmarshal([]byte(parametersSchema), &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(expected), &want); err != nil {
		t.Fatal(err)
	}
	properties, ok := got["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema properties is not an object")
	}
	for _, property := range properties {
		field, ok := property.(map[string]any)
		if !ok {
			t.Fatal("schema property is not an object")
		}
		delete(field, "description")
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("non-description schema contract changed: got=%#v want=%#v", got, want)
	}
}

func TestSearchProviderSpecsShareEvidenceBasedDescriptions(t *testing.T) {
	t.Parallel()
	// Construction and Spec only: neither provider is executed or networked.
	tavily, err := New("offline-spec-only-not-a-real-key")
	if err != nil {
		t.Fatal(err)
	}
	searx, err := newSearXNG("http://127.0.0.1:8888", false)
	if err != nil {
		t.Fatal(err)
	}
	left, right := tavily.Spec(), searx.Spec()
	if left.Name != "search_web" || right.Name != left.Name || !left.ReadOnly || !right.ReadOnly ||
		left.Description != right.Description || string(left.Parameters) != string(right.Parameters) {
		t.Errorf("provider tool contracts differ: Tavily=%#v SearXNG=%#v", left, right)
	}
	for _, required := range []string{"concise targeted queries", "named candidate", "missing facts", "user-specified", "evidence-identified"} {
		if !strings.Contains(left.Description, required) {
			t.Errorf("provider description lacks verification guidance %q", required)
		}
	}
}

func TestResearchExtractionSpecChangesOnlyAdvertisedModeEnumAndDescription(t *testing.T) {
	t.Parallel()
	baseline, err := New("offline-schema-fixture")
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name         string
		config       Config
		onlyResearch bool
	}{
		{"searx_research", Config{Provider: ProviderSearXNG, ResearchExtraction: true}, true},
		{"searx_discovery", Config{Provider: ProviderSearXNG}, false},
		{"searx_fallback", Config{Provider: ProviderSearXNG, ResearchExtraction: true, TavilyFallback: true, TavilyAPIKey: "offline-schema-fixture"}, true},
		{"tavily", Config{Provider: ProviderTavily, ResearchExtraction: true, TavilyAPIKey: "offline-schema-fixture"}, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			searcher, err := NewConfigured(scenario.config)
			if err != nil {
				t.Fatal(err)
			}
			gotSpec, wantSpec := searcher.Spec(), baseline.Spec()
			gotSpec.Parameters, wantSpec.Parameters = nil, nil
			if !reflect.DeepEqual(gotSpec, wantSpec) {
				t.Errorf("tool metadata changed alongside the mode enum: got=%#v want=%#v", gotSpec, wantSpec)
			}
			var got, want map[string]any
			if err := json.Unmarshal(searcher.Spec().Parameters, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(parametersSchema), &want); err != nil {
				t.Fatal(err)
			}
			if scenario.onlyResearch {
				mode := want["properties"].(map[string]any)["mode"].(map[string]any)
				mode["enum"] = []any{"research"}
				mode["description"] = "Use research to discover sources and fetch pages for verification."
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("model schema changed beyond the intended mode enum and description: got=%#v want=%#v", got, want)
			}
		})
	}
}
