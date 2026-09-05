package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

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
		input := body["input"].([]any)
		user := input[1].(map[string]any)
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

	extractor, err := NewMemoryExtractor("test-key", "test-model")
	if err != nil {
		t.Fatalf("NewMemoryExtractor() error = %v", err)
	}
	extractor.endpoint = server.URL
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

func TestLiveMemoryCorrectionExtraction(t *testing.T) {
	if os.Getenv("RUN_LIVE_ASSISTANT_TEST") != "1" {
		t.Skip("set RUN_LIVE_ASSISTANT_TEST=1 to call the live OpenAI API")
	}
	model := strings.TrimSpace(os.Getenv("OPENAI_MEMORY_MODEL"))
	if model == "" {
		model = requiredLiveEnv(t, "OPENAI_ROUTER_MODEL")
	}
	extractor, err := NewMemoryExtractor(requiredLiveEnv(t, "OPENAI_API_KEY"), model)
	if err != nil {
		t.Fatalf("NewMemoryExtractor() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	candidates, err := extractor.Extract(ctx, "Change my protein target to 150 grams.")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1: %#v", len(candidates), candidates)
	}
	candidate := candidates[0]
	if candidate.MemoryKey != "profile.nutrition.daily_protein_target" ||
		candidate.Retention != memory.RetentionDurable ||
		!strings.Contains(candidate.Card.Summary, "150") {
		t.Fatalf("correction candidate = %#v", candidate)
	}
	t.Logf("correction candidate: %#v", candidate)
}

func TestLiveStateTransitionExtraction(t *testing.T) {
	if os.Getenv("RUN_LIVE_ASSISTANT_TEST") != "1" {
		t.Skip("set RUN_LIVE_ASSISTANT_TEST=1 to call the live OpenAI API")
	}
	model := strings.TrimSpace(os.Getenv("OPENAI_MEMORY_MODEL"))
	if model == "" {
		model = requiredLiveEnv(t, "OPENAI_ROUTER_MODEL")
	}
	extractor, err := NewMemoryExtractor(requiredLiveEnv(t, "OPENAI_API_KEY"), model)
	if err != nil {
		t.Fatalf("NewMemoryExtractor() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	candidates, err := extractor.Extract(ctx, "I just left the gym.")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1: %#v", len(candidates), candidates)
	}
	candidate := candidates[0]
	if candidate.MemoryKey != "state.activity.current" ||
		candidate.Retention != memory.RetentionTemporary ||
		!strings.Contains(strings.ToLower(candidate.Card.Summary), "gym") {
		t.Fatalf("transition candidate = %#v", candidate)
	}
	t.Logf("transition candidate: %#v", candidate)
}

func workoutMemoryCandidates() []map[string]any {
	type item struct {
		key       string
		title     string
		summary   string
		topic     string
		kind      string
		retention string
	}
	items := []item{
		{"state.activity.current", "Currently working out", "The user is currently working out.", "health", "event", "temporary"},
		{"profile.nutrition.daily_calorie_range", "Daily calorie range", "The user typically needs 2,000 to 3,000 calories per day.", "health", "goal", "durable"},
		{"profile.nutrition.daily_protein_target", "Daily protein target", "The user targets 130 grams of protein per day.", "health", "goal", "durable"},
		{"profile.food.flavor_preference", "Prefers flavorful meals", "The user loves flavorful meals.", "preferences", "preference", "durable"},
		{"profile.food.preference.steak", "Likes steak", "The user likes meals with steak.", "preferences", "preference", "durable"},
		{"profile.food.preference.chicken", "Likes chicken", "The user likes meals with chicken.", "preferences", "preference", "durable"},
		{"profile.food.preference.pasta", "Likes pasta", "The user likes meals with pasta.", "preferences", "preference", "durable"},
		{"profile.food.preference.rice", "Likes rice", "The user likes meals with rice.", "preferences", "preference", "durable"},
		{"profile.role.student", "Is a student", "The user is a student.", "personal", "fact", "durable"},
		{"profile.health.weight", "Approximate weight", "The user weighs around 150 pounds.", "health", "fact", "durable"},
	}
	result := make([]map[string]any, 0, len(items))
	for _, candidate := range items {
		result = append(result, map[string]any{
			"memory_key": candidate.key,
			"retention":  candidate.retention,
			"card": map[string]any{
				"topics":   []string{candidate.topic},
				"kind":     candidate.kind,
				"title":    candidate.title,
				"summary":  candidate.summary,
				"details":  []any{},
				"entities": []any{},
			},
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
	extractor, err := NewMemoryExtractor("test-key", "test-model")
	if err != nil {
		t.Fatalf("NewMemoryExtractor() error = %v", err)
	}
	extractor.endpoint = "http://127.0.0.1:1/should-not-be-called"
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
