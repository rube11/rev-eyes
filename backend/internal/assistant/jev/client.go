// Package jev provides the assistant's Go client for TypeSafe's Jev API.
package jev

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
	endpoint            = "https://api.typesafe.ai/v1/systemone"
	defaultModel        = "jev-latest"
	maxResponseBodySize = 4 << 20
)

var (
	ErrAPIKeyRequired     = errors.New("JEV_API_KEY is required")
	ErrHTTPClientRequired = errors.New("Jev HTTP client is required")
)

type QuestionType string

const (
	QuestionNoul   QuestionType = "noul"
	QuestionChoice QuestionType = "choice"
	QuestionScore  QuestionType = "score"
)

// Question asks Jev for one judgment about the shared state. Criteria is
// optional for Noul, an option map for Choice, and ordered levels for Score.
type Question struct {
	Type         QuestionType `json:"type"`
	Instructions any          `json:"instructions"`
	Criteria     any          `json:"criteria,omitempty"`
}

type Request struct {
	State     any                 `json:"state"`
	Model     string              `json:"model"`
	Questions map[string]Question `json:"questions"`
}

type Answer struct {
	Type          QuestionType       `json:"type"`
	Noul          float64            `json:"noul,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Score         float64            `json:"score,omitempty"`
	Legend        map[string]string  `json:"legend,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   Usage             `json:"usage"`
}

type Client struct {
	apiKey   string
	http     *http.Client
	endpoint string
}

func New(apiKey string) (*Client, error) {
	return NewClient(apiKey, &http.Client{Timeout: 10 * time.Second})
}

func NewClient(apiKey string, httpClient *http.Client) (*Client, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, ErrAPIKeyRequired
	}
	if httpClient == nil {
		return nil, ErrHTTPClientRequired
	}
	return &Client{apiKey: apiKey, http: httpClient, endpoint: endpoint}, nil
}

func (c *Client) Evaluate(ctx context.Context, input Request) (Response, error) {
	if strings.TrimSpace(input.Model) == "" {
		input.Model = defaultModel
	}
	body, err := json.Marshal(input)
	if err != nil {
		return Response{}, fmt.Errorf("encode Jev request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("create Jev request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Content-Type", "application/json")

	response, err := c.http.Do(request)
	if err != nil {
		return Response{}, fmt.Errorf("send Jev request: %w", err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBodySize+1))
	if err != nil {
		return Response{}, fmt.Errorf("read Jev response: %w", err)
	}
	if len(responseBody) > maxResponseBodySize {
		return Response{}, errors.New("Jev response exceeded size limit")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Response{}, fmt.Errorf(
			"Jev returned HTTP %d: %s",
			response.StatusCode,
			strings.TrimSpace(string(responseBody)),
		)
	}

	var result Response
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return Response{}, fmt.Errorf("decode Jev response: %w", err)
	}
	return result, nil
}
