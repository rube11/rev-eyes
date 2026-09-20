// Package responses contains the shared transport for OpenAI's Responses API.
// Higher-level packages own prompts and interpretation; this package only owns
// request encoding, HTTP behavior, and output parsing.
package responses

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
	defaultEndpoint     = "https://api.openai.com/v1/responses"
	maxResponseBodySize = 4 << 20
)

// Client calls a single configured OpenAI model.
type Client struct {
	apiKey     string
	model      string
	httpClient *http.Client
	endpoint   string
}

// Config overrides transport defaults, primarily for tests.
type Config struct {
	HTTPClient *http.Client
	Endpoint   string
}

func New(apiKey, model string, config ...Config) (Client, error) {
	apiKey, model = strings.TrimSpace(apiKey), strings.TrimSpace(model)
	if apiKey == "" {
		return Client{}, errors.New("OpenAI API key is required")
	}
	if model == "" {
		return Client{}, errors.New("OpenAI model is required")
	}

	client := Client{
		apiKey:     apiKey,
		model:      model,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		endpoint:   defaultEndpoint,
	}
	if len(config) > 0 {
		if config[0].HTTPClient != nil {
			client.httpClient = config[0].HTTPClient
		}
		if endpoint := strings.TrimSpace(config[0].Endpoint); endpoint != "" {
			client.endpoint = endpoint
		}
	}
	return client, nil
}

type Options struct {
	Instructions     string
	Text             map[string]any
	MaxOutputTokens  int
	IncludeReasoning bool
}

type Response struct {
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	Output []json.RawMessage `json:"output"`
}

type Call struct {
	CallID    string
	Name      string
	Arguments json.RawMessage
}

type createRequest struct {
	Model           string            `json:"model"`
	Instructions    string            `json:"instructions"`
	Input           []json.RawMessage `json:"input"`
	Text            map[string]any    `json:"text,omitempty"`
	MaxOutputTokens int               `json:"max_output_tokens,omitempty"`
	Store           bool              `json:"store"`
	Include         []string          `json:"include,omitempty"`
}

type outputItem struct {
	Type      string `json:"type"`
	Phase     string `json:"phase"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	} `json:"content"`
}

func Message(role, content string) (json.RawMessage, error) {
	return json.Marshal(struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}{Role: role, Content: content})
}

func (c *Client) Create(ctx context.Context, input []json.RawMessage, options Options) (Response, error) {
	body := createRequest{
		Model:           c.model,
		Instructions:    options.Instructions,
		Input:           input,
		Text:            options.Text,
		MaxOutputTokens: options.MaxOutputTokens,
		Store:           false,
	}
	if options.IncludeReasoning {
		body.Include = []string{"reasoning.encrypted_content"}
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("encode OpenAI request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(encoded))
	if err != nil {
		return Response{}, fmt.Errorf("create OpenAI request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return Response{}, fmt.Errorf("send OpenAI request: %w", err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBodySize))
	if err != nil {
		return Response{}, fmt.Errorf("read OpenAI response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Response{}, statusError(response.StatusCode, responseBody)
	}

	var result Response
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return Response{}, fmt.Errorf("decode OpenAI response: %w", err)
	}
	if result.Error != nil {
		return Response{}, fmt.Errorf("OpenAI response failed: %s", result.Error.Message)
	}
	return result, nil
}

func ParseOutput(output []json.RawMessage) ([]Call, string, error) {
	var calls []Call
	var text []string
	for _, raw := range output {
		var item outputItem
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, "", fmt.Errorf("decode OpenAI output: %w", err)
		}
		switch item.Type {
		case "function_call":
			if strings.TrimSpace(item.CallID) == "" || strings.TrimSpace(item.Name) == "" {
				return nil, "", errors.New("OpenAI returned an invalid tool call")
			}
			calls = append(calls, Call{CallID: item.CallID, Name: item.Name, Arguments: json.RawMessage(item.Arguments)})
		case "message":
			for _, content := range item.Content {
				if content.Refusal != "" {
					return nil, "", fmt.Errorf("OpenAI refused response: %s", content.Refusal)
				}
				if item.Phase == "commentary" {
					continue
				}
				if content.Type == "output_text" && strings.TrimSpace(content.Text) != "" {
					text = append(text, strings.TrimSpace(content.Text))
				}
			}
		}
	}
	return calls, strings.Join(text, "\n"), nil
}

func statusError(statusCode int, body []byte) error {
	var response struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &response) == nil && response.Error.Message != "" {
		return fmt.Errorf("OpenAI API returned status %d: %s", statusCode, response.Error.Message)
	}
	return fmt.Errorf("OpenAI API returned status %d", statusCode)
}
