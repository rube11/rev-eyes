package extraction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/assistant/jev"
	"github.com/rube11/rev-eyes/backend/internal/assistant/openai/responses"
	"github.com/rube11/rev-eyes/backend/internal/memory"
)

const workoutMemoryUtterance = "Hey, I am working out. I typically tend to need to eat around 2,000 to 3,000 calories and 130 grams of protein a day. I also love flavorful meals with steak, chicken, pasta, and rice. I am around 150 pounds, and I am also a student."

func TestMemoryExtractorDecodesAtomicWorkoutCandidates(t *testing.T) {
	encodedOutput, err := json.Marshal(map[string]any{
		"memories": workoutMemoryCandidates(),
	})
	if err != nil {
		t.Fatalf("encode output: %v", err)
	}

	requestResult := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var handlerErr error
		defer func() { requestResult <- handlerErr }()
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			handlerErr = fmt.Errorf("decode request: %w", err)
			http.Error(w, handlerErr.Error(), http.StatusBadRequest)
			return
		}
		if body["model"] != "test-model" || body["store"] != false {
			handlerErr = fmt.Errorf("request = %#v", body)
			http.Error(w, handlerErr.Error(), http.StatusBadRequest)
			return
		}
		if body["instructions"] != memoryExtractorPrompt {
			handlerErr = fmt.Errorf("instructions were not supplied")
			http.Error(w, handlerErr.Error(), http.StatusBadRequest)
			return
		}
		input := body["input"].([]any)
		user := input[0].(map[string]any)
		if user["content"] != workoutMemoryUtterance {
			handlerErr = fmt.Errorf("user content = %#v", user["content"])
			http.Error(w, handlerErr.Error(), http.StatusBadRequest)
			return
		}
		handlerErr = json.NewEncoder(w).Encode(map[string]any{
			"output": []map[string]any{{
				"type": "message",
				"content": []map[string]any{{
					"type": "output_text",
					"text": string(encodedOutput),
				}},
			}},
		})
	}))
	defer server.Close()

	classifier := workoutMetadataEvaluator()
	extractor, err := NewMemoryExtractor("test-key", "test-model", classifier, responses.Config{HTTPClient: server.Client(), Endpoint: server.URL})
	if err != nil {
		t.Fatalf("NewMemoryExtractor() error = %v", err)
	}
	candidates, err := extractor.Extract(context.Background(), workoutMemoryUtterance)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if err := <-requestResult; err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 10 {
		t.Fatalf("candidate count = %d, want 10", len(candidates))
	}
	if classifier.calls != 1 {
		t.Fatalf("Jev calls = %d, want 1", classifier.calls)
	}

	byKey := make(map[string]memory.Candidate, len(candidates))
	for _, candidate := range candidates {
		byKey[candidate.MemoryKey] = candidate
	}
	wantKeys := []string{
		"state.activity.current",
		"profile.nutrition.daily_calorie_range",
		"profile.nutrition.daily_protein_target",
		"profile.food.flavor_preference",
		"profile.food.preference.steak",
		"profile.food.preference.chicken",
		"profile.food.preference.pasta",
		"profile.food.preference.rice",
		"profile.role.student",
		"profile.health.weight",
	}
	for _, key := range wantKeys {
		if _, found := byKey[key]; !found {
			t.Errorf("missing key %q", key)
		}
	}
	activity := byKey["state.activity.current"]
	if activity.Retention != memory.RetentionTemporary || activity.ExpiresAt != nil {
		t.Fatalf("activity lifecycle = %#v", activity)
	}

	encodedCandidates, err := json.Marshal(candidates)
	if err != nil {
		t.Fatalf("encode candidates: %v", err)
	}
	lower := strings.ToLower(string(encodedCandidates))
	for _, invented := range []string{"budget", "allerg", "muscle", "gender", "dietary restriction"} {
		if strings.Contains(lower, invented) {
			t.Errorf("candidates contain invented concept %q", invented)
		}
	}
}

func workoutMemoryCandidates() []map[string]any {
	type item struct {
		key       string
		title     string
		summary   string
		topic     string
		kind      string
		profile   string
		retention string
	}
	items := []item{
		{"state.activity.current", "Currently working out", "The user is currently working out.", "health", "event", "recent", "temporary"},
		{"profile.nutrition.daily_calorie_range", "Daily calorie range", "The user typically needs 2,000 to 3,000 calories per day.", "health", "goal", "core", "durable"},
		{"profile.nutrition.daily_protein_target", "Daily protein target", "The user targets 130 grams of protein per day.", "health", "goal", "core", "durable"},
		{"profile.food.flavor_preference", "Prefers flavorful meals", "The user loves flavorful meals.", "preferences", "preference", "core", "durable"},
		{"profile.food.preference.steak", "Likes steak", "The user likes meals with steak.", "preferences", "preference", "detail", "durable"},
		{"profile.food.preference.chicken", "Likes chicken", "The user likes meals with chicken.", "preferences", "preference", "detail", "durable"},
		{"profile.food.preference.pasta", "Likes pasta", "The user likes meals with pasta.", "preferences", "preference", "detail", "durable"},
		{"profile.food.preference.rice", "Likes rice", "The user likes meals with rice.", "preferences", "preference", "detail", "durable"},
		{"profile.role.student", "Is a student", "The user is a student.", "personal", "fact", "core", "durable"},
		{"profile.health.weight", "Approximate weight", "The user weighs around 150 pounds.", "health", "fact", "detail", "durable"},
	}
	result := make([]map[string]any, 0, len(items))
	for _, candidate := range items {
		result = append(result, map[string]any{
			"memory_key": candidate.key,
			"title":      candidate.title,
			"summary":    candidate.summary,
			"details":    []any{},
			"entities":   []any{},
		})
	}
	return result
}

func TestMemoryExtractorPromptRejectsAmbientAndSecretData(t *testing.T) {
	for _, rule := range []string{
		"passwords, authentication codes, API keys",
		"ambient speech, filler, quoted media",
		"facts only about another person",
		"My roommate says she hates mushrooms",
		"profile.nutrition.daily_protein_target",
		"Save only direct user statements",
		"I just left the gym",
	} {
		if !strings.Contains(memoryExtractorPrompt, rule) {
			t.Errorf("memoryExtractorPrompt missing %q", rule)
		}
	}
}

func TestMemoryExtractorSkipsCredentialUtterancesBeforeModelCall(t *testing.T) {
	extractor, err := NewMemoryExtractor("test-key", "test-model", &fakeMemoryJev{})
	if err != nil {
		t.Fatalf("NewMemoryExtractor() error = %v", err)
	}
	candidates, err := extractor.Extract(
		context.Background(),
		"Remember that my password is hunter2.",
	)
	if !errors.Is(err, memory.ErrUnsafeMemory) {
		t.Fatalf("Extract() error = %v, want unsafe-memory error", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates = %#v, want none", candidates)
	}
}

type fakeMemoryJev struct {
	response jev.Response
	err      error
	request  jev.Request
	calls    int
}

func (f *fakeMemoryJev) Evaluate(_ context.Context, request jev.Request) (jev.Response, error) {
	f.calls++
	f.request = request
	if f.response.Answers == nil {
		f.response.Answers = make(map[string]jev.Answer)
	}
	for id, question := range request.Questions {
		if _, found := f.response.Answers[id]; !found {
			f.response.Answers[id] = jev.Answer{Type: question.Type}
		}
	}
	return f.response, f.err
}

func workoutMetadataEvaluator() *fakeMemoryJev {
	answers := make(map[string]jev.Answer)
	items := workoutMemoryCandidates()
	for index, raw := range items {
		key := raw["memory_key"].(string)
		retention := memory.RetentionDurable
		profile := memory.ProfileDetail
		kind := memory.KindPreference
		topic := memory.TopicPreferences
		switch {
		case key == "state.activity.current":
			retention, profile, kind, topic = memory.RetentionTemporary, memory.ProfileRecent, memory.KindEvent, memory.TopicHealth
		case strings.Contains(key, "nutrition"):
			profile, kind, topic = memory.ProfileCore, memory.KindGoal, memory.TopicHealth
		case key == "profile.food.flavor_preference":
			profile = memory.ProfileCore
		case strings.HasPrefix(key, "profile.role"):
			profile, kind, topic = memory.ProfileCore, memory.KindFact, memory.TopicPersonal
		case key == "profile.health.weight":
			kind, topic = memory.KindFact, memory.TopicHealth
		}
		answers[retentionQuestion(index)] = jev.Answer{Choice: string(retention)}
		answers[profileLayerQuestion(index)] = jev.Answer{Choice: string(profile)}
		answers[keyFamilyQuestion(index)] = jev.Answer{Choice: memoryKeyFamilyForTest(key)}
		answers[kindQuestion(index)] = jev.Answer{Choice: string(kind)}
		answers[topicQuestion(index, string(topic))] = jev.Answer{Noul: 0.95}
	}
	return &fakeMemoryJev{response: jev.Response{Answers: answers}}
}

func memoryKeyFamilyForTest(key string) string {
	for _, family := range []string{memoryKeyFoodPreference, memoryKeyRole, memoryKeyRelationship} {
		if strings.HasPrefix(key, family+".") {
			return family
		}
	}
	for _, family := range []string{
		memoryKeyActivity, memoryKeyCalorieTarget, memoryKeyCalorieRange,
		memoryKeyProteinTarget, memoryKeyFlavor, memoryKeyWeight,
	} {
		if key == family {
			return family
		}
	}
	return memoryKeyCustom
}
