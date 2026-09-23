package extraction

import (
	"strings"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/assistant/jev"
	"github.com/rube11/rev-eyes/backend/internal/memory"
)

func TestOpenAIExtractionSchemaContainsOnlyGroundedDraftFields(t *testing.T) {
	schema := memoryCandidateSchema()
	properties := schema["properties"].(map[string]any)
	for _, forbidden := range []string{"topics", "kind", "retention", "profile_layer", "card"} {
		if _, exists := properties[forbidden]; exists {
			t.Errorf("OpenAI draft schema still classifies %q", forbidden)
		}
	}
	for _, required := range []string{"memory_key", "title", "summary", "details", "entities"} {
		if _, exists := properties[required]; !exists {
			t.Errorf("OpenAI draft schema is missing %q", required)
		}
	}
	for _, want := range []string{"same memory_key", "Do not choose the memory-key family", "typed Jev pass", "source-grounded"} {
		if !strings.Contains(memoryExtractorPrompt, want) {
			t.Errorf("missing atomizer rule %q", want)
		}
	}
}

func TestJevOwnsMemoryMetadataClassification(t *testing.T) {
	drafts := []memoryDraft{{
		MemoryKey: "profile.role.student",
		Title:     "Student",
		Summary:   "The user is a student.",
		Entities:  []memoryDraftName{{Name: "State University"}},
	}}
	questions := memoryClassificationQuestions(drafts)
	for _, id := range []string{
		keyFamilyQuestion(0),
		retentionQuestion(0),
		profileLayerQuestion(0),
		kindQuestion(0),
		topicQuestion(0, string(memory.TopicPersonal)),
		entityTypeQuestion(0, 0),
	} {
		if _, exists := questions[id]; !exists {
			t.Errorf("missing Jev metadata question %q", id)
		}
	}
	if questions[retentionQuestion(0)].Type != jev.QuestionChoice ||
		questions[topicQuestion(0, string(memory.TopicPersonal))].Type != jev.QuestionNoul {
		t.Fatal("memory metadata uses the wrong Jev primitive")
	}
}
