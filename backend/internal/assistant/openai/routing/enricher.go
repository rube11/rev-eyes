// Package routing enriches an action already selected by Jev with retrieval
// inputs. It cannot change the selected action or execute tools.
package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/rube11/rev-eyes/backend/internal/assistant"
	"github.com/rube11/rev-eyes/backend/internal/assistant/openai/responses"
	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
)

const enrichmentPrompt = `The application has already selected the user's action.
Do not classify intent and do not change the supplied action. Your only job is to prepare a concise query and structured memory lookup for that fixed action.

Use recent_dialogue only to resolve references and conversational repairs. Never treat an earlier turn as a new command.

Set query to a concise, standalone version of the latest request. Set memory_review_all=true only when the fixed action is memory_review and the user asks to review their overall profile; then leave query and memory_lookup empty. Set it false otherwise.

For respond, state_transition, memory_review, memory_forget, profile_include, and profile_exclude, create a focused memory lookup when the user names a subject.
For respond and state_transition, always create a proactive memory lookup:
- terms: one to five short lowercase words or phrases likely to occur in a relevant memory title or summary. Prefer stable concepts such as "protein target", "food preference", or "manager" over surface wording.
- topics: zero to three relevant memory topics.
- kinds: zero or more relevant memory kinds.
- entities: names explicitly mentioned in the request.
Text terms and exact entities are the strongest retrieval signals. Include a topic and kind together only when both are useful.

For remember, query is the durable fact the user asked to save.
For state_transition, query describes the completed activity and asks for at most one timely next step grounded in supplied context. Search for context that can decide the next move: after exercise, nutrition targets, recent intake, and food preferences; after study or an exam, commitments, deadlines, instructions, and priorities.
For a specific memory_review, include useful alternative terms for the same concept without inventing facts. For an overall profile review, leave query and lookup empty and set memory_review_all=true.
For memory_forget, query is the specific fact when supplied. Resolve "that" only when recent dialogue identifies exactly one fact; otherwise leave query and lookup empty.
For memory_correct, query is the complete replacement fact when supplied; otherwise it is empty.
For profile_include and profile_exclude, query and lookup identify only the existing fact, not the command words. Resolve "this" or "that" only when recent dialogue identifies exactly one fact.
Return only the requested enrichment fields. The action is immutable and deliberately absent from the output schema.`

type Enricher struct {
	client responses.Client
}

func New(apiKey, model string, config ...responses.Config) (*Enricher, error) {
	client, err := responses.New(apiKey, model, config...)
	if err != nil {
		return nil, err
	}
	return &Enricher{client: client}, nil
}

func (e *Enricher) Enrich(ctx context.Context, action assistant.Action, utterance string, conversation session.Conversation) (assistant.Decision, error) {
	input, err := enrichmentInput(action, utterance, conversation)
	if err != nil {
		return assistant.Decision{}, fmt.Errorf("encode router enrichment input: %w", err)
	}
	message, err := responses.Message("user", input)
	if err != nil {
		return assistant.Decision{}, fmt.Errorf("encode router enrichment message: %w", err)
	}
	response, err := e.client.Create(ctx, []json.RawMessage{message}, responses.Options{
		Instructions: enrichmentPrompt,
		Text:         enrichmentTextFormat(),
	})
	if err != nil {
		return assistant.Decision{}, err
	}
	calls, text, err := responses.ParseOutput(response.Output)
	if err != nil {
		return assistant.Decision{}, err
	}
	if len(calls) > 0 {
		return assistant.Decision{}, errors.New("router enricher cannot execute tools")
	}
	if strings.TrimSpace(text) == "" {
		return assistant.Decision{}, errors.New("OpenAI response contained no router enrichment")
	}
	var decision assistant.Decision
	if err := json.Unmarshal([]byte(text), &decision); err != nil {
		return assistant.Decision{}, fmt.Errorf("decode router enrichment: %w", err)
	}
	decision.Action = action
	return decision, nil
}

func enrichmentInput(action assistant.Action, utterance string, conversation session.Conversation) (string, error) {
	encoded, err := json.Marshal(struct {
		Action          assistant.Action         `json:"action"`
		RecentDialogue  []assistant.ResponseTurn `json:"recent_dialogue,omitempty"`
		LatestUtterance string                   `json:"latest_utterance"`
	}{
		Action:          action,
		RecentDialogue:  assistant.BoundedRoutingDialogue(conversation),
		LatestUtterance: strings.TrimSpace(utterance),
	})
	return string(encoded), err
}

func enrichmentTextFormat() map[string]any {
	return map[string]any{"format": map[string]any{
		"type": "json_schema", "name": "router_enrichment", "strict": true,
		"schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":             map[string]string{"type": "string"},
				"memory_lookup":     memoryLookupSchema(),
				"memory_review_all": map[string]any{"type": "boolean"},
			},
			"required": []string{"query", "memory_lookup", "memory_review_all"}, "additionalProperties": false,
		},
	}}
}

func memoryLookupSchema() map[string]any {
	terms := stringArraySchema(nil)
	terms["maxItems"] = 5
	topics := stringArraySchema(memory.TopicValues())
	topics["maxItems"] = 3
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"terms": terms, "topics": topics,
			"kinds": stringArraySchema(memory.KindValues()), "entities": stringArraySchema(nil),
		},
		"required": []string{"terms", "topics", "kinds", "entities"}, "additionalProperties": false,
	}
}

func stringArraySchema(values []string) map[string]any {
	items := map[string]any{"type": "string"}
	if len(values) > 0 {
		items["enum"] = values
	}
	return map[string]any{"type": "array", "items": items}
}
