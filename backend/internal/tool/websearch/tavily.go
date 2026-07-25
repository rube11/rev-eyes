package websearch

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
	"unicode/utf8"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const (
	searchURL           = "https://api.tavily.com/search"
	maxQueryLength      = 400
	maxResults          = 5
	maxTitleLength      = 300
	maxURLLength        = 2048
	maxSnippetLength    = 600
	maxResponseBodySize = 1 << 20
)

const parametersSchema = `{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "A precise natural-language question preserving key names, dates, and locations. Do not use a keyword list or add phrases such as public web."
    }
  },
  "required": ["query"],
  "additionalProperties": false
}`

var (
	ErrAPIKeyRequired = errors.New("TAVILY_API_KEY is required")
	ErrNoResults      = errors.New("web search returned no usable results")
)

// Tool searches the public web through Tavily.
type Tool struct {
	apiKey   string
	client   *http.Client
	endpoint string
}

// Result is one normalized Tavily search result.
type Result struct {
	Title         string  `json:"title"`
	URL           string  `json:"url"`
	Snippet       string  `json:"snippet"`
	Score         float64 `json:"score"`
	PublishedDate string  `json:"published_date,omitempty"`
}

func New(apiKey string) (*Tool, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, ErrAPIKeyRequired
	}
	return &Tool{
		apiKey:   apiKey,
		client:   &http.Client{Timeout: 10 * time.Second},
		endpoint: searchURL,
	}, nil
}

func (t *Tool) Spec() tool.Spec {
	return tool.Spec{
		Name:        "search_web",
		Description: "Search the current public web using a precise natural-language question.",
		Parameters:  json.RawMessage(parametersSchema),
		ReadOnly:    true,
	}
}

func (t *Tool) Execute(
	ctx context.Context,
	_ tool.Scope,
	arguments json.RawMessage,
) (tool.Result, error) {
	var input struct {
		Query string `json:"query"`
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return tool.Result{}, fmt.Errorf("decode web search: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return tool.Result{}, errors.New("decode web search: expected one JSON object")
	}

	input.Query = strings.TrimSpace(input.Query)
	if err := validateQuery(input.Query); err != nil {
		return tool.Result{}, err
	}
	results, err := t.search(ctx, searchRequest{
		Query:          input.Query,
		AutoParameters: true,
		SearchDepth:    "fast",
		MaxResults:     maxResults,
	})
	if err != nil {
		return tool.Result{}, err
	}
	if len(results) == 0 {
		return tool.Result{}, ErrNoResults
	}
	encoded, err := json.Marshal(struct {
		Results []Result `json:"results"`
	}{Results: results})
	if err != nil {
		return tool.Result{}, fmt.Errorf("encode web search results: %w", err)
	}
	return tool.Result{Content: string(encoded)}, nil
}

type searchRequest struct {
	Query          string `json:"query"`
	AutoParameters bool   `json:"auto_parameters"`
	SearchDepth    string `json:"search_depth"`
	MaxResults     int    `json:"max_results"`
	Topic          string `json:"topic,omitempty"`
	TimeRange      string `json:"time_range,omitempty"`
}

// SearchNews performs a bounded one-day news search for background watches.
func (t *Tool) SearchNews(ctx context.Context, query string) ([]Result, error) {
	query = strings.TrimSpace(query)
	if err := validateQuery(query); err != nil {
		return nil, err
	}
	return t.search(ctx, searchRequest{
		Query:       query,
		SearchDepth: "basic",
		MaxResults:  maxResults,
		Topic:       "news",
		TimeRange:   "day",
	})
}

func (t *Tool) search(ctx context.Context, parameters searchRequest) ([]Result, error) {
	body, err := json.Marshal(parameters)
	if err != nil {
		return nil, fmt.Errorf("encode Tavily search: %w", err)
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		t.endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("create Tavily search: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+t.apiKey)
	request.Header.Set("Content-Type", "application/json")

	response, err := t.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send Tavily search: %w", err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBodySize))
	if err != nil {
		return nil, fmt.Errorf("read Tavily search: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Tavily search returned status %d", response.StatusCode)
	}

	var payload struct {
		Results []struct {
			Title         string  `json:"title"`
			URL           string  `json:"url"`
			Content       string  `json:"content"`
			Score         float64 `json:"score"`
			PublishedDate string  `json:"published_date"`
		} `json:"results"`
	}
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return nil, fmt.Errorf("decode Tavily search: %w", err)
	}
	if len(payload.Results) > maxResults {
		payload.Results = payload.Results[:maxResults]
	}

	results := make([]Result, 0, len(payload.Results))
	for _, item := range payload.Results {
		title := strings.TrimSpace(item.Title)
		url := strings.TrimSpace(item.URL)
		if title == "" || url == "" || len(url) > maxURLLength {
			continue
		}
		results = append(results, Result{
			Title:         truncate(title, maxTitleLength),
			URL:           url,
			Snippet:       truncate(item.Content, maxSnippetLength),
			Score:         item.Score,
			PublishedDate: strings.TrimSpace(item.PublishedDate),
		})
	}
	return results, nil
}

func validateQuery(query string) error {
	if query == "" {
		return errors.New("web search query is required")
	}
	if utf8.RuneCountInString(query) > maxQueryLength {
		return errors.New("web search query is too long")
	}
	return nil
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}
