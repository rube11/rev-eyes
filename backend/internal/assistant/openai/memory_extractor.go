package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/memory"
)

const (
	maxExtractedMemories = 12
)

const memoryExtractorPrompt = `You are the private memory-learning pass for a wearable assistant. Read one finalized USER utterance and extract only personal context that will genuinely improve future help.

Return zero to twelve atomic memories. Each memory must contain exactly one independently updateable fact, preference, goal, relationship, instruction, or current state. Split lists and numeric targets into separate memories. Use only what the user actually said; never infer a budget, diagnosis, motive, identity, goal, allergy, restriction, or relationship.

Memory keys use this canonical taxonomy; do not invent synonyms for these families:
- state.activity.current
- profile.nutrition.daily_calorie_target or profile.nutrition.daily_calorie_range
- profile.nutrition.daily_protein_target
- profile.food.flavor_preference
- profile.food.preference.<normalized_food>
- profile.role.<normalized_role>
- profile.health.weight
- profile.relationship.<normalized_person_or_role>
Use another short lowercase path only when none of these families fits. Reuse the exact same key when a later utterance changes the same fact.

Use retention=temporary for a current activity or short-lived situation and durable for stable profile information. Save only direct user statements; do not turn hedged, hypothetical, or inferred claims into facts.

Do not save:
- passwords, authentication codes, API keys, payment-card or bank details;
- ambient speech, filler, quoted media, assistant text, or facts only about another person;
- hypothetical or uncertain possibilities as facts;
- the wording of a request when it contains no reusable personal context.

Examples:
- "I'm working out" becomes one temporary state.activity.current memory.
- "I just left the gym" becomes one temporary state.activity.current memory saying the workout ended and the user left the gym.
- "I aim for 130 grams of protein and like steak and chicken" becomes three durable memories: the protein target, steak preference, and chicken preference.
- "Change my protein target to 150 grams" becomes one durable memory using profile.nutrition.daily_protein_target, replacing the older value.
- "My roommate says she hates mushrooms" does not become the user's food preference.

Write concise standalone cards. Do not respond to the user and do not include reasoning.`

// MemoryExtractor turns a finalized user utterance into atomic memory
// candidates. It is intentionally independent of response routing.
type MemoryExtractor struct {
	apiKey   string
	model    string
	endpoint string
	client   *http.Client
}

func NewMemoryExtractor(apiKey, model string) (*MemoryExtractor, error) {
	apiKey = strings.TrimSpace(apiKey)
	model = strings.TrimSpace(model)
	if apiKey == "" {
		return nil, errors.New("OpenAI API key is required")
	}
	if model == "" {
		return nil, errors.New("OpenAI memory model is required")
	}
	return &MemoryExtractor{
		apiKey:   apiKey,
		model:    model,
		endpoint: responsesURL,
		client:   &http.Client{Timeout: 15 * time.Second},
	}, nil
}

func (e *MemoryExtractor) Extract(
	ctx context.Context,
	utterance string,
) ([]memory.Candidate, error) {
	utterance = strings.TrimSpace(utterance)
	if utterance == "" {
		return nil, nil
	}
	if containsBlockedSecret(utterance) {
		return nil, memory.ErrUnsafeMemory
	}
	body, err := json.Marshal(memoryExtractorRequest(e.model, utterance))
	if err != nil {
		return nil, fmt.Errorf("encode memory extraction request: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		e.endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("create memory extraction request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+e.apiKey)
	request.Header.Set("Content-Type", "application/json")

	response, err := e.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send memory extraction request: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBodySize))
	if err != nil {
		return nil, fmt.Errorf("read memory extraction response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, classifierStatusError(response.StatusCode, responseBody)
	}

	var result classifierResponse
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return nil, fmt.Errorf("decode memory extraction response: %w", err)
	}
	var outputText string
	for _, output := range result.Output {
		for _, content := range output.Content {
			if content.Refusal != "" {
				return nil, fmt.Errorf("OpenAI refused memory extraction: %s", content.Refusal)
			}
			if content.Type == "output_text" && strings.TrimSpace(content.Text) != "" {
				outputText = content.Text
				break
			}
		}
	}
	if strings.TrimSpace(outputText) == "" {
		return nil, errors.New("OpenAI response contained no memory extraction")
	}

	var extracted struct {
		Memories []struct {
			MemoryKey string           `json:"memory_key"`
			Retention memory.Retention `json:"retention"`
			Card      memory.Card      `json:"card"`
		} `json:"memories"`
	}
	if err := json.Unmarshal([]byte(outputText), &extracted); err != nil {
		return nil, fmt.Errorf("decode extracted memories: %w", err)
	}
	if len(extracted.Memories) > maxExtractedMemories {
		return nil, fmt.Errorf("OpenAI returned too many memory candidates: %d", len(extracted.Memories))
	}

	candidates := make([]memory.Candidate, 0, len(extracted.Memories))
	for index, item := range extracted.Memories {
		candidate := memory.Candidate{
			Card:      item.Card,
			MemoryKey: item.MemoryKey,
			Retention: item.Retention,
		}
		candidate = candidate.Normalize()
		if candidateContainsBlockedSecret(candidate) {
			return nil, fmt.Errorf("memory candidate %d: %w", index, memory.ErrUnsafeMemory)
		}
		if candidate.MemoryKey == "" {
			return nil, fmt.Errorf("memory candidate %d has no memory key", index)
		}
		if err := candidate.Validate(); err != nil {
			return nil, fmt.Errorf("validate memory candidate %d: %w", index, err)
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func candidateContainsBlockedSecret(candidate memory.Candidate) bool {
	parts := []string{candidate.MemoryKey, candidate.Card.Title, candidate.Card.Summary}
	for _, detail := range candidate.Card.Details {
		parts = append(parts, detail.Key, detail.Value)
	}
	for _, entity := range candidate.Card.Entities {
		parts = append(parts, string(entity.Type), entity.Name)
	}
	return containsBlockedSecret(strings.Join(parts, " "))
}

func containsBlockedSecret(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"password",
		"passcode",
		"verification code",
		"one-time code",
		"otp code",
		"api key",
		"secret key",
		"credit card",
		"card number",
		"bank account",
		"routing number",
		"social security number",
		" cvv",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func memoryExtractorRequest(model, utterance string) map[string]any {
	return map[string]any{
		"model": model,
		"input": []map[string]string{
			{"role": "system", "content": memoryExtractorPrompt},
			{"role": "user", "content": utterance},
		},
		"max_output_tokens": 2400,
		"store":             false,
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "memory_candidates",
				"strict": true,
				"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"memories": map[string]any{
							"type":     "array",
							"maxItems": maxExtractedMemories,
							"items":    memoryCandidateSchema(),
						},
					},
					"required":             []string{"memories"},
					"additionalProperties": false,
				},
			},
		},
	}
}

func memoryCandidateSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"memory_key": map[string]string{"type": "string"},
			"retention": map[string]any{
				"type": "string",
				"enum": []string{string(memory.RetentionDurable), string(memory.RetentionTemporary)},
			},
			"card": memoryCardSchema(),
		},
		"required": []string{
			"memory_key",
			"retention",
			"card",
		},
		"additionalProperties": false,
	}
}

func memoryCardSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"topics": stringArraySchema(memory.TopicValues()),
			"kind": map[string]any{
				"type": "string",
				"enum": memory.KindValues(),
			},
			"title":   map[string]string{"type": "string"},
			"summary": map[string]string{"type": "string"},
			"details": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"key":   map[string]string{"type": "string"},
						"value": map[string]string{"type": "string"},
					},
					"required":             []string{"key", "value"},
					"additionalProperties": false,
				},
			},
			"entities": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"type": map[string]any{
							"type": "string",
							"enum": memory.EntityTypeValues(),
						},
						"name": map[string]string{"type": "string"},
					},
					"required":             []string{"type", "name"},
					"additionalProperties": false,
				},
			},
		},
		"required": []string{
			"topics",
			"kind",
			"title",
			"summary",
			"details",
			"entities",
		},
		"additionalProperties": false,
	}
}
