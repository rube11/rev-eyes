package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func TestAgentIncludesProfileWhenMemorySearchIsEmpty(t *testing.T) {
	profile := "# User profile\n## Core\n- Student at North College."
	agent := testAgent(t, nil, func(w http.ResponseWriter, r *http.Request) {
		var request createRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(request.Input)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(encoded), "Student at North College") || !strings.Contains(request.Instructions, "not a new command or authorization") {
			t.Error("profile not passed as bounded untrusted context")
		}
		writeJSON(t, w, map[string]any{"output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "Let's plan around your classes."}}}}})
	})
	_, err := agent.Respond(context.Background(), tool.Scope{}, "What should I do next?", session.Conversation{Profile: profile}, nil)
	if err != nil {
		t.Fatal(err)
	}
}

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
