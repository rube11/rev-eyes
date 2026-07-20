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

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const (
	responsesURL        = "https://api.openai.com/v1/responses"
	maxResponseBodySize = 4 << 20
)

type inputMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type functionTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict"`
}

type createRequest struct {
	Model             string            `json:"model"`
	Instructions      string            `json:"instructions"`
	Input             []json.RawMessage `json:"input"`
	Tools             []functionTool    `json:"tools,omitempty"`
	ToolChoice        string            `json:"tool_choice,omitempty"`
	ParallelToolCalls bool              `json:"parallel_tool_calls"`
	Store             bool              `json:"store"`
	Include           []string          `json:"include,omitempty"`
}

type createResponse struct {
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	Output []json.RawMessage `json:"output"`
}

type outputItem struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	} `json:"content"`
}

type toolCall struct {
	CallID    string
	Name      string
	Arguments json.RawMessage
}

type toolOutput struct {
	Type   string `json:"type"`
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

func (a *Agent) createResponse(
	ctx context.Context,
	input []json.RawMessage,
	tools []functionTool,
) (createResponse, error) {
	body := createRequest{
		Model:             a.model,
		Instructions:      agentInstructions,
		Input:             input,
		Tools:             tools,
		ParallelToolCalls: true,
		Store:             false,
		Include:           []string{"reasoning.encrypted_content"},
	}
	if len(tools) > 0 {
		body.ToolChoice = "auto"
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return createResponse{}, fmt.Errorf("encode OpenAI request: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		a.endpoint,
		bytes.NewReader(encoded),
	)
	if err != nil {
		return createResponse{}, fmt.Errorf("create OpenAI request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+a.apiKey)
	request.Header.Set("Content-Type", "application/json")

	response, err := a.client.Do(request)
	if err != nil {
		return createResponse{}, fmt.Errorf("send OpenAI request: %w", err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBodySize))
	if err != nil {
		return createResponse{}, fmt.Errorf("read OpenAI response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return createResponse{}, apiStatusError(response.StatusCode, responseBody)
	}

	var result createResponse
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return createResponse{}, fmt.Errorf("decode OpenAI response: %w", err)
	}
	if result.Error != nil {
		return createResponse{}, fmt.Errorf("OpenAI response failed: %s", result.Error.Message)
	}
	return result, nil
}

func toolDefinitions(specs []tool.Spec) ([]functionTool, error) {
	definitions := make([]functionTool, 0, len(specs))
	for _, spec := range specs {
		if len(spec.Parameters) == 0 || !json.Valid(spec.Parameters) {
			return nil, fmt.Errorf("tool %q has an invalid JSON schema", spec.Name)
		}
		definitions = append(definitions, functionTool{
			Type:        "function",
			Name:        spec.Name,
			Description: strings.TrimSpace(spec.Description),
			Parameters:  spec.Parameters,
			Strict:      true,
		})
	}
	return definitions, nil
}

func parseOutput(output []json.RawMessage) ([]toolCall, string, error) {
	var calls []toolCall
	var text []string

	for _, raw := range output {
		var item outputItem
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, "", fmt.Errorf("decode OpenAI output: %w", err)
		}

		switch item.Type {
		case "function_call":
			if strings.TrimSpace(item.CallID) == "" ||
				strings.TrimSpace(item.Name) == "" {
				return nil, "", errors.New("OpenAI returned an invalid tool call")
			}
			calls = append(calls, toolCall{
				CallID:    item.CallID,
				Name:      item.Name,
				Arguments: json.RawMessage(item.Arguments),
			})
		case "message":
			for _, content := range item.Content {
				if content.Refusal != "" {
					return nil, "", fmt.Errorf("OpenAI refused response: %s", content.Refusal)
				}
				if content.Type == "output_text" &&
					strings.TrimSpace(content.Text) != "" {
					text = append(text, strings.TrimSpace(content.Text))
				}
			}
		}
	}

	return calls, strings.Join(text, "\n"), nil
}

func apiStatusError(statusCode int, body []byte) error {
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
