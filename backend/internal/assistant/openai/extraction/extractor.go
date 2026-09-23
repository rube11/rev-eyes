// Package extraction turns finalized user utterances into private, atomic
// memory candidates. It is independent from response routing and composition.
package extraction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/rube11/rev-eyes/backend/internal/assistant/jev"
	"github.com/rube11/rev-eyes/backend/internal/assistant/openai/responses"
	"github.com/rube11/rev-eyes/backend/internal/memory"
)

const maxExtractedMemories = 12

const memoryExtractorPrompt = `You are the private memory-learning pass for a wearable assistant. Read one finalized USER utterance and extract only personal context that will genuinely improve future help.

Return zero to twelve atomic memory drafts. Each draft must contain exactly one independently updateable fact, preference, goal, relationship, instruction, or current state. Split lists and numeric targets into separate drafts. Use only what the user actually said; never infer a budget, diagnosis, motive, identity, goal, allergy, restriction, or relationship.

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

Save only direct user statements; do not turn hedged, hypothetical, or inferred claims into facts. A changed value keeps the same memory_key. Only use facts explicitly supplied by the user. Pinning/excluding existing memories is handled separately, not by inventing a new memory.

Do not choose the memory-key family, topics, kind, retention, profile placement, or entity types. A separate typed Jev pass owns those classifications. Supply a canonical key hint so code can preserve a source-grounded suffix after Jev selects the family. Extract only source-grounded wording, details, and entity names. Entities are named people, places, organizations, projects, or events; do not emit foods, activities, measurements, or other common nouns as entities.

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

Write concise standalone drafts. Do not respond to the user and do not include reasoning.`

const memoryTopicThreshold = 0.5

const (
	memoryKeyActivity       = "state.activity.current"
	memoryKeyCalorieTarget  = "profile.nutrition.daily_calorie_target"
	memoryKeyCalorieRange   = "profile.nutrition.daily_calorie_range"
	memoryKeyProteinTarget  = "profile.nutrition.daily_protein_target"
	memoryKeyFlavor         = "profile.food.flavor_preference"
	memoryKeyFoodPreference = "profile.food.preference"
	memoryKeyRole           = "profile.role"
	memoryKeyWeight         = "profile.health.weight"
	memoryKeyRelationship   = "profile.relationship"
	memoryKeyCustom         = "custom"
)

type jevEvaluator interface {
	Evaluate(context.Context, jev.Request) (jev.Response, error)
}

// MemoryExtractor turns a finalized user utterance into atomic memory
// candidates. It is intentionally independent of response routing.
type MemoryExtractor struct {
	client    responses.Client
	evaluator jevEvaluator
}

func NewMemoryExtractor(apiKey, model string, evaluator jevEvaluator, config ...responses.Config) (*MemoryExtractor, error) {
	client, err := responses.New(apiKey, model, config...)
	if err != nil {
		return nil, err
	}
	if evaluator == nil {
		return nil, errors.New("memory Jev evaluator is required")
	}
	return &MemoryExtractor{client: client, evaluator: evaluator}, nil
}

type memoryDraft struct {
	MemoryKey string            `json:"memory_key"`
	Title     string            `json:"title"`
	Summary   string            `json:"summary"`
	Details   []memory.Detail   `json:"details"`
	Entities  []memoryDraftName `json:"entities"`
}

type memoryDraftName struct {
	Name string `json:"name"`
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
	input, err := responses.Message("user", utterance)
	if err != nil {
		return nil, fmt.Errorf("encode memory extraction input: %w", err)
	}
	response, err := e.client.Create(ctx, []json.RawMessage{input}, responses.Options{
		Instructions:    memoryExtractorPrompt,
		Text:            memoryExtractorTextFormat(),
		MaxOutputTokens: 2400,
	})
	if err != nil {
		return nil, err
	}
	calls, outputText, err := responses.ParseOutput(response.Output)
	if err != nil {
		return nil, err
	}
	if len(calls) > 0 {
		return nil, errors.New("memory extractor cannot execute tools")
	}
	if strings.TrimSpace(outputText) == "" {
		return nil, errors.New("OpenAI response contained no memory extraction")
	}

	var extracted struct {
		Memories []memoryDraft `json:"memories"`
	}
	if err := json.Unmarshal([]byte(outputText), &extracted); err != nil {
		return nil, fmt.Errorf("decode extracted memories: %w", err)
	}
	if len(extracted.Memories) > maxExtractedMemories {
		return nil, fmt.Errorf("OpenAI returned too many memory candidates: %d", len(extracted.Memories))
	}

	if len(extracted.Memories) == 0 {
		return nil, nil
	}
	return e.classify(ctx, utterance, extracted.Memories)
}

func (e *MemoryExtractor) classify(
	ctx context.Context,
	utterance string,
	drafts []memoryDraft,
) ([]memory.Candidate, error) {
	questions := memoryClassificationQuestions(drafts)
	response, err := e.evaluator.Evaluate(ctx, jev.Request{
		State: map[string]any{
			"source_utterance": utterance,
			"candidates":       drafts,
		},
		Questions: questions,
	})
	if err != nil {
		return nil, fmt.Errorf("classify memory metadata with Jev: %w", err)
	}
	for id := range questions {
		if _, found := response.Answers[id]; !found {
			return nil, fmt.Errorf("Jev memory classification omitted answer %q", id)
		}
	}

	candidates := make([]memory.Candidate, 0, len(drafts))
	for index, draft := range drafts {
		memoryKey, keyErr := classifiedMemoryKey(draft, response.Answers[keyFamilyQuestion(index)].Choice)
		if keyErr != nil {
			return nil, fmt.Errorf("classify memory key %d: %w", index, keyErr)
		}
		entities := make([]memory.Entity, 0, len(draft.Entities))
		for entityIndex, entity := range draft.Entities {
			entities = append(entities, memory.Entity{
				Type: memory.EntityType(response.Answers[entityTypeQuestion(index, entityIndex)].Choice),
				Name: entity.Name,
			})
		}
		candidate := memory.Candidate{
			MemoryKey:    memoryKey,
			Retention:    memory.Retention(response.Answers[retentionQuestion(index)].Choice),
			ProfileLayer: memory.ProfileLayer(response.Answers[profileLayerQuestion(index)].Choice),
			Card: memory.Card{
				Topics:   classifiedTopics(response, index),
				Kind:     memory.Kind(response.Answers[kindQuestion(index)].Choice),
				Title:    draft.Title,
				Summary:  draft.Summary,
				Details:  draft.Details,
				Entities: entities,
			},
		}.Normalize()
		if candidateContainsBlockedSecret(candidate) {
			return nil, fmt.Errorf("memory candidate %d: %w", index, memory.ErrUnsafeMemory)
		}
		if candidate.MemoryKey == "" {
			return nil, fmt.Errorf("memory candidate %d has no memory key", index)
		}
		if err := candidate.Validate(); err != nil {
			return nil, fmt.Errorf("validate Jev-classified memory candidate %d: %w", index, err)
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func memoryClassificationQuestions(drafts []memoryDraft) map[string]jev.Question {
	questions := make(map[string]jev.Question)
	for index, draft := range drafts {
		reference := fmt.Sprintf("`candidates[%d]`", index)
		questions[retentionQuestion(index)] = jev.Question{
			Type:         jev.QuestionChoice,
			Instructions: fmt.Sprintf("How long should the personal memory in %s remain useful? Classify its meaning, not how recently it was spoken.", reference),
			Criteria: map[string]any{
				string(memory.RetentionTemporary): "A current activity or short-lived situation useful for the next interaction; examples include currently working out or having just arrived somewhere.",
				string(memory.RetentionDurable):   "Stable personal context, including a fact, preference, relationship, instruction, role, or ongoing goal.",
			},
		}
		questions[keyFamilyQuestion(index)] = jev.Question{
			Type:         jev.QuestionChoice,
			Instructions: fmt.Sprintf("Which canonical memory-key family describes the personal memory in %s? Choose custom only when none of the named families fits.", reference),
			Criteria: map[string]any{
				memoryKeyActivity:       "A current activity or immediate activity transition.",
				memoryKeyCalorieTarget:  "One exact daily calorie target.",
				memoryKeyCalorieRange:   "A daily calorie range with a lower and upper value.",
				memoryKeyProteinTarget:  "A daily protein target.",
				memoryKeyFlavor:         "A general flavor or intensity preference spanning foods.",
				memoryKeyFoodPreference: "A like, dislike, or preference about one particular food or dish; code adds the food suffix.",
				memoryKeyRole:           "The user's role, occupation, or identity in a context; code adds the role suffix.",
				memoryKeyWeight:         "The user's body weight.",
				memoryKeyRelationship:   "The user's relationship to a named person or role; code adds the person or role suffix.",
				memoryKeyCustom:         "A reusable personal memory that fits none of the named canonical families.",
			},
		}
		questions[profileLayerQuestion(index)] = jev.Question{
			Type:         jev.QuestionChoice,
			Instructions: fmt.Sprintf("Which profile layer should contain the personal memory in %s? Judge its future importance, not its position in the input.", reference),
			Criteria: map[string]any{
				string(memory.ProfileCore):   "Durable context that would materially change help across many future requests, such as identity, role, important relationships, active goals, numeric targets, or strong standing instructions.",
				string(memory.ProfileRecent): "A temporary current activity or short-lived situation useful for the next interaction.",
				string(memory.ProfileDetail): "Useful searchable information that need not be present every turn; the default for isolated facts, events, and individual likes.",
			},
		}
		kindCriteria := make(map[string]any, len(memory.KindValues()))
		for _, value := range memory.KindValues() {
			kindCriteria[value] = nil
		}
		questions[kindQuestion(index)] = jev.Question{
			Type:         jev.QuestionChoice,
			Instructions: fmt.Sprintf("Which single memory kind best describes the personal memory in %s?", reference),
			Criteria:     kindCriteria,
		}
		for _, topic := range memory.TopicValues() {
			questions[topicQuestion(index, topic)] = jev.Question{
				Type:         jev.QuestionNoul,
				Instructions: fmt.Sprintf("Should the personal memory in %s be tagged with topic %q?", reference, topic),
				Criteria: map[string]string{
					"true":  "The topic directly describes a useful way to retrieve this memory.",
					"false": "The topic is incidental, overly broad, or unrelated.",
				},
			}
		}
		for entityIndex := range draft.Entities {
			entityCriteria := make(map[string]any, len(memory.EntityTypeValues()))
			for _, value := range memory.EntityTypeValues() {
				entityCriteria[value] = nil
			}
			questions[entityTypeQuestion(index, entityIndex)] = jev.Question{
				Type: jev.QuestionChoice,
				Instructions: fmt.Sprintf(
					"Which entity type best describes `candidates[%d].entities[%d].name` in this memory?",
					index, entityIndex,
				),
				Criteria: entityCriteria,
			}
		}
	}
	return questions
}

func classifiedTopics(response jev.Response, candidateIndex int) []memory.Topic {
	type scoredTopic struct {
		topic memory.Topic
		score float64
	}
	scores := make([]scoredTopic, 0, len(memory.TopicValues()))
	for _, topic := range memory.TopicValues() {
		scores = append(scores, scoredTopic{
			topic: memory.Topic(topic),
			score: response.Answers[topicQuestion(candidateIndex, topic)].Noul,
		})
	}
	sort.SliceStable(scores, func(left, right int) bool { return scores[left].score > scores[right].score })
	topics := make([]memory.Topic, 0, 3)
	for _, candidate := range scores {
		if candidate.score < memoryTopicThreshold || len(topics) == 3 {
			break
		}
		topics = append(topics, candidate.topic)
	}
	if len(topics) == 0 {
		return []memory.Topic{memory.TopicOther}
	}
	return topics
}

func classifiedMemoryKey(draft memoryDraft, family string) (string, error) {
	switch family {
	case memoryKeyActivity, memoryKeyCalorieTarget, memoryKeyCalorieRange,
		memoryKeyProteinTarget, memoryKeyFlavor, memoryKeyWeight:
		return family, nil
	case memoryKeyFoodPreference, memoryKeyRole, memoryKeyRelationship:
		suffix := ""
		if strings.HasPrefix(draft.MemoryKey, family+".") {
			suffix = strings.TrimPrefix(draft.MemoryKey, family+".")
		}
		if suffix == "" {
			suffix = memoryKeySuffix(draft.Title)
		}
		if suffix == "" {
			return "", errors.New("dynamic memory-key family has no usable suffix")
		}
		return family + "." + suffix, nil
	case memoryKeyCustom:
		if strings.TrimSpace(draft.MemoryKey) == "" {
			return "", errors.New("custom memory has no key hint")
		}
		return draft.MemoryKey, nil
	default:
		return "", fmt.Errorf("unsupported Jev memory-key family %q", family)
	}
}

func memoryKeySuffix(value string) string {
	parts := strings.FieldsFunc(strings.ToLower(value), func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsDigit(character)
	})
	return strings.Join(parts, "_")
}

func retentionQuestion(index int) string    { return fmt.Sprintf("candidate_%d_retention", index) }
func profileLayerQuestion(index int) string { return fmt.Sprintf("candidate_%d_profile_layer", index) }
func keyFamilyQuestion(index int) string    { return fmt.Sprintf("candidate_%d_key_family", index) }
func kindQuestion(index int) string         { return fmt.Sprintf("candidate_%d_kind", index) }
func topicQuestion(index int, topic string) string {
	return fmt.Sprintf("candidate_%d_topic_%s", index, topic)
}
func entityTypeQuestion(index, entityIndex int) string {
	return fmt.Sprintf("candidate_%d_entity_%d_type", index, entityIndex)
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

func memoryExtractorTextFormat() map[string]any {
	return map[string]any{
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
	}
}

func memoryCandidateSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"memory_key": map[string]string{"type": "string"},
			"title":      map[string]string{"type": "string"},
			"summary":    map[string]string{"type": "string"},
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
						"name": map[string]string{"type": "string"},
					},
					"required":             []string{"name"},
					"additionalProperties": false,
				},
			},
		},
		"required": []string{
			"memory_key",
			"title",
			"summary",
			"details",
			"entities",
		},
		"additionalProperties": false,
	}
}
