package extraction

import (
	"strings"
	"testing"
)

func TestProfileExtractionSchemaAndPrompt(t *testing.T) {
	schema := memoryCandidateSchema()
	props := schema["properties"].(map[string]any)
	field, ok := props["profile_layer"].(map[string]any)
	if !ok || len(field["enum"].([]string)) != 3 {
		t.Fatal("missing profile enum")
	}
	found := false
	for _, key := range schema["required"].([]string) {
		if key == "profile_layer" {
			found = true
		}
	}
	if !found {
		t.Fatal("profile layer must be required in strict schema")
	}
	for _, want := range []string{"core:", "recent:", "detail:", "same extraction", "when in doubt", "same memory_key"} {
		if !strings.Contains(memoryExtractorPrompt, want) {
			t.Errorf("missing rule %q", want)
		}
	}
}
