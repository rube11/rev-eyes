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

const routerPrompt = `You classify finalized speech for a wearable assistant.

Choose exactly one action:
- ignore: background speech, filler, or speech that requires no processing.
- respond: a direct question or command that should wake the main assistant.
- state_update: current conversational or situational context that should update short-lived assistant state without a response.
- remember: an explicit user request to remember a durable fact or preference. Never choose remember unless the user explicitly asks for it.
- propose_task: a potential task inferred from the speech that should be proposed to the user before execution.
- propose_watch: a request or strong implied interest in monitoring a future public update over time.

Set query to a concise, standalone version of the request. Use an empty query for ignore.
Set memory_lookup to empty arrays unless the action is respond, propose_task, or propose_watch.
For respond, propose_task, and propose_watch, always create a proactive memory lookup:
- terms: one to five short lowercase words or phrases likely to appear in a relevant memory title or summary. Include useful synonyms, not filler words.
- topics: zero to three relevant memory topics.
- kinds: zero or more relevant memory kinds.
- entities: names explicitly mentioned in the request.
Topics and kinds are hard filters, so leave them empty when uncertain.
Set memory to null unless the action is remember.
For remember, set query to the memory summary and create one memory card:
- Choose one to three topics from: work, personal, friends, family, relationships, health, preferences, goals, places, other.
- Choose a kind from: fact, preference, relationship, event, goal, instruction.
- Write a short title and a single concise, standalone summary.
- Add useful details as lowercase snake_case keys with short values.
- Include named people, places, organizations, projects, and events as entities.
- Use only facts stated in the utterance. Never resolve missing context or invent details.
For propose_task, preserve whether the user implied the action rather than explicitly requested it in query.
Choose propose_watch only when future web information must be checked repeatedly, not for a one-time current-information question.
Classify the speech only. Do not answer it.`

// NewClassifier creates the function used by the activity router.
func NewClassifier(apiKey, model string) (func(context.Context, string) (string, error), error) {
	apiKey = strings.TrimSpace(apiKey)
	model = strings.TrimSpace(model)
	if apiKey == "" {
		return nil, errors.New("OpenAI API key is required")
	}
	if model == "" {
		return nil, errors.New("OpenAI model is required")
	}

	client := &http.Client{Timeout: 10 * time.Second}

	return func(ctx context.Context, utterance string) (string, error) {
		body, err := json.Marshal(classifierRequest(model, utterance))
		if err != nil {
			return "", fmt.Errorf("encode OpenAI request: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, responsesURL, bytes.NewReader(body))
		if err != nil {
			return "", fmt.Errorf("create OpenAI request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Content-Type", "application/json")

		response, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("send OpenAI request: %w", err)
		}
		defer response.Body.Close()

		responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil {
			return "", fmt.Errorf("read OpenAI response: %w", err)
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return "", classifierStatusError(response.StatusCode, responseBody)
		}

		var result classifierResponse
		if err := json.Unmarshal(responseBody, &result); err != nil {
			return "", fmt.Errorf("decode OpenAI response: %w", err)
		}

		for _, output := range result.Output {
			for _, content := range output.Content {
				if content.Refusal != "" {
					return "", fmt.Errorf("OpenAI refused router classification: %s", content.Refusal)
				}
				if content.Type == "output_text" && strings.TrimSpace(content.Text) != "" {
					return content.Text, nil
				}
			}
		}

		return "", errors.New("OpenAI response contained no classification")
	}, nil
}

func classifierRequest(model, utterance string) map[string]any {
	return map[string]any{
		"model": model,
		"input": []map[string]string{
			{"role": "system", "content": routerPrompt},
			{"role": "user", "content": utterance},
		},
		"store": false,
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "router_decision",
				"strict": true,
				"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{
							"type": "string",
							"enum": []string{"ignore", "respond", "state_update", "remember", "propose_task", "propose_watch"},
						},
						"query":         map[string]string{"type": "string"},
						"memory_lookup": memoryLookupSchema(),
						"memory": map[string]any{
							"anyOf": []any{
								memoryCardSchema(),
								map[string]string{"type": "null"},
							},
						},
					},
					"required":             []string{"action", "query", "memory_lookup", "memory"},
					"additionalProperties": false,
				},
			},
		},
	}
}

func memoryLookupSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"terms":    stringArraySchema(nil),
			"topics":   stringArraySchema(memory.TopicValues()),
			"kinds":    stringArraySchema(memory.KindValues()),
			"entities": stringArraySchema(nil),
		},
		"required":             []string{"terms", "topics", "kinds", "entities"},
		"additionalProperties": false,
	}
}

func stringArraySchema(values []string) map[string]any {
	items := map[string]any{"type": "string"}
	if len(values) > 0 {
		items["enum"] = values
	}
	return map[string]any{"type": "array", "items": items}
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

type classifierResponse struct {
	Output []struct {
		Content []struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Refusal string `json:"refusal"`
		} `json:"content"`
	} `json:"output"`
}

func classifierStatusError(statusCode int, body []byte) error {
	var response struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &response); err == nil && response.Error.Message != "" {
		return fmt.Errorf("OpenAI API returned status %d: %s", statusCode, response.Error.Message)
	}
	return fmt.Errorf("OpenAI API returned status %d", statusCode)
}
