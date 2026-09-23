package extraction

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/assistant/jev"
	"github.com/rube11/rev-eyes/backend/internal/assistant/openai/responses"
	"github.com/rube11/rev-eyes/backend/internal/memory"
)

func TestProfileMetadataMismatchPreservesValidFacts(t *testing.T) {
	output, err := json.Marshal(map[string]any{"memories": []memoryDraft{
		{MemoryKey: "profile.role.student", Title: "Student", Summary: "The user is a student.", Details: []memory.Detail{}, Entities: []memoryDraftName{}},
		{MemoryKey: "state.activity.current", Title: "Workout", Summary: "The user just left the gym.", Details: []memory.Detail{}, Entities: []memoryDraftName{}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": string(output)}}}}})
	}))
	defer server.Close()
	classifier := &fakeMemoryJev{response: jev.Response{Answers: map[string]jev.Answer{
		keyFamilyQuestion(0):                           {Choice: memoryKeyRole},
		retentionQuestion(0):                           {Choice: string(memory.RetentionDurable)},
		profileLayerQuestion(0):                        {Choice: string(memory.ProfileCore)},
		kindQuestion(0):                                {Choice: string(memory.KindFact)},
		topicQuestion(0, string(memory.TopicPersonal)): {Noul: 0.9},
		keyFamilyQuestion(1):                           {Choice: memoryKeyActivity},
		retentionQuestion(1):                           {Choice: string(memory.RetentionTemporary)},
		profileLayerQuestion(1):                        {Choice: string(memory.ProfileCore)},
		kindQuestion(1):                                {Choice: string(memory.KindEvent)},
		topicQuestion(1, string(memory.TopicHealth)):   {Noul: 0.9},
	}}}
	extractor, err := NewMemoryExtractor("synthetic-key", "test-model", classifier, responses.Config{HTTPClient: server.Client(), Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	got, err := extractor.Extract(context.Background(), "I'm a student and I just left the gym.")
	if err != nil || len(got) != 2 {
		t.Fatalf("valid facts discarded with mismatched profile metadata: candidates=%d error=%v", len(got), err)
	}
	if got[0].ProfileLayer != memory.ProfileCore || got[1].ProfileLayer != memory.ProfileRecent || got[1].Retention != memory.RetentionTemporary {
		t.Fatal("normalization changed lifecycle or misplaced facts")
	}
}
