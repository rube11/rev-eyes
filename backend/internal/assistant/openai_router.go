package assistant

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
)

const (
	openAIResponsesURL = "https://api.openai.com/v1/responses"
	routerPrompt       = `You classify finalized speech for a wearable assistant.

Choose exactly one action:
- ignore: background speech, filler, or speech that requires no processing.
- respond: a direct question or command that should wake the main assistant.
- state_update: current conversational or situational context that should update short-lived assistant state without a response.
- remember: a durable fact, preference, or explicit request to remember something.
- propose_task: a potential task inferred from the speech that should be proposed to the user before execution.

Set query to a concise, standalone version of the request. Use an empty query for ignore.
Classify the speech only. Do not answer it.`
)

// NewOpenAIClassifier creates the function used by Router to classify utterances.
func NewOpenAIClassifier(apiKey, model string) (func(context.Context, string) (string, error), error) {
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
		body, err := json.Marshal(openAIRequest(model, utterance))
		if err != nil {
			return "", fmt.Errorf("encode OpenAI request: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIResponsesURL, bytes.NewReader(body))
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
			return "", openAIStatusError(response.StatusCode, responseBody)
		}

		var result openAIResponse
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

func openAIRequest(model, utterance string) map[string]any {
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
							"enum": []string{"ignore", "respond", "state_update", "remember", "propose_task"},
						},
						"query": map[string]string{"type": "string"},
					},
					"required":             []string{"action", "query"},
					"additionalProperties": false,
				},
			},
		},
	}
}

type openAIResponse struct {
	Output []struct {
		Content []struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Refusal string `json:"refusal"`
		} `json:"content"`
	} `json:"output"`
}

func openAIStatusError(statusCode int, body []byte) error {
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
