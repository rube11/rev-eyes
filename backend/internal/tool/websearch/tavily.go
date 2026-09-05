package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const (
	searchURL              = "https://api.tavily.com/search"
	maxQueryLength         = 400
	quickMaxResults        = 5
	researchMaxResults     = 8
	maxTitleLength         = 300
	maxURLLength           = 2048
	maxSnippetLength       = 1600
	maxPublishedDateLength = 100
	maxDomainFilters       = 5
	maxResponseBodySize    = 4 << 20
)

const parametersSchema = `{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "Concise search keywords preserving relevant names, dates, locations, budgets, and preferences; omit conversational filler and companion names. For verification, use a named candidate already found plus the missing fact, such as menu prices or opening hours. Put site restrictions in include_domains, not query."
    },
    "mode": {
      "type": "string",
      "enum": ["quick", "research"],
      "description": "Use quick only to discover possible sources; it does not fetch source pages. Use research for any answer requiring source verification, including every follow-up for prices, hours, schedules, rules, or scientific/technical claims."
    },
    "topic": {
      "type": "string",
      "enum": ["general", "news"],
      "description": "Use news only for recent events covered by news sources; otherwise use general."
    },
    "recency": {
      "type": "string",
      "enum": ["none", "day", "week", "month", "year"],
      "description": "Limit result age only when freshness is part of the request. Use none for local recommendations, evergreen facts, and official guidance."
    },
    "include_domains": {
      "type": "array",
      "items": { "type": "string" },
      "maxItems": 5,
      "description": "Optional real bare hostnames containing a dot. Use an empty array for discovery or when the operator is unknown. Restrict to a site only when supplied by the user or identified by retrieved evidence; never guess an authoritative domain. If a filtered lookup returns no useful evidence, try the named candidate without a domain filter; do not repeat the same failed restriction."
    }
  },
  "required": ["query", "mode", "topic", "recency", "include_domains"],
  "additionalProperties": false
}`

var (
	ErrAPIKeyRequired = errors.New("TAVILY_API_KEY is required")
	ErrNoResults      = errors.New("web search returned no usable results")
)

var domainPattern = regexp.MustCompile(
	`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`,
)

type searchInput struct {
	Query          string   `json:"query"`
	Mode           string   `json:"mode"`
	Topic          string   `json:"topic"`
	Recency        string   `json:"recency"`
	IncludeDomains []string `json:"include_domains"`
}

// Tool searches the public web through Tavily.
type Tool struct {
	apiKey   string
	client   *http.Client
	endpoint string
}

// Result is one normalized search result or separately cited linked evidence page.
type Result struct {
	Title             string   `json:"title"`
	URL               string   `json:"url"`
	Snippet           string   `json:"snippet,omitempty"`
	Score             float64  `json:"score"`
	PublishedDate     string   `json:"published_date,omitempty"`
	DiscoveredFrom    string   `json:"discovered_from,omitempty"`
	DiscoverySnippet  string   `json:"discovery_snippet,omitempty"`
	PageExcerpts      []string `json:"page_excerpts,omitempty"`
	PagePublishedDate string   `json:"page_published_date,omitempty"`
	ExtractionStatus  string   `json:"extraction_status,omitempty"`
}

type searchResponse struct {
	Results             []Result `json:"results"`
	Query               string   `json:"query"`
	Mode                string   `json:"mode"`
	Topic               string   `json:"topic"`
	Recency             string   `json:"recency"`
	ResponseTime        string   `json:"response_time,omitempty"`
	RequestID           string   `json:"request_id,omitempty"`
	Credits             int      `json:"credits,omitempty"`
	Provider            string   `json:"provider"`
	LatencyMS           int64    `json:"latency_ms"`
	ResponsiveEngines   int      `json:"responsive_engines,omitempty"`
	UnresponsiveEngines int      `json:"unresponsive_engines,omitempty"`
	ExtractedResults    int      `json:"extracted_results,omitempty"`
}

func New(apiKey string) (*Tool, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, ErrAPIKeyRequired
	}
	return &Tool{
		apiKey:   apiKey,
		client:   &http.Client{Timeout: 20 * time.Second},
		endpoint: searchURL,
	}, nil
}

func (t *Tool) Spec() tool.Spec {
	return tool.Spec{
		Name:        "search_web",
		Description: "Search current public-web evidence using concise targeted queries. Discover candidates or verify a named candidate's missing facts; use domain filters only for user-specified or evidence-identified sites.",
		Parameters:  json.RawMessage(parametersSchema),
		ReadOnly:    true,
	}
}

func (t *Tool) Execute(
	ctx context.Context,
	_ tool.Scope,
	arguments json.RawMessage,
) (tool.Result, error) {
	input, err := decodeSearchInput(arguments)
	if err != nil {
		return tool.Result{}, err
	}
	started := time.Now()

	request := searchRequest{
		Query:          input.Query,
		SearchDepth:    "basic",
		MaxResults:     quickMaxResults,
		Topic:          input.Topic,
		TimeRange:      searchTimeRange(input.Recency),
		IncludeDomains: input.IncludeDomains,
		Language:       "en",
		FilterLanguage: true,
		SafeSearch:     true,
		IncludeUsage:   true,
	}
	if input.Mode == "research" {
		request.SearchDepth = "advanced"
		request.ChunksPerSource = 3
		request.MaxResults = researchMaxResults
	}

	response, err := t.search(ctx, request)
	if err != nil {
		t.logSearch(ctx, input, time.Since(started), 0, 0, err)
		return tool.Result{}, err
	}
	if len(response.Results) == 0 {
		t.logSearch(ctx, input, time.Since(started), 0, response.Credits, ErrNoResults)
		return tool.Result{}, ErrNoResults
	}
	response.Query = input.Query
	response.Mode = input.Mode
	response.Topic = input.Topic
	response.Recency = input.Recency
	response.Provider = ProviderTavily
	response.LatencyMS = time.Since(started).Milliseconds()
	encoded, err := json.Marshal(response)
	if err != nil {
		return tool.Result{}, fmt.Errorf("encode web search results: %w", err)
	}
	t.logSearch(ctx, input, time.Since(started), len(response.Results), response.Credits, nil)
	return tool.Result{Content: string(encoded)}, nil
}

func decodeSearchInput(arguments json.RawMessage) (searchInput, error) {
	var input searchInput
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return searchInput{}, fmt.Errorf("decode web search: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return searchInput{}, errors.New("decode web search: expected one JSON object")
	}
	return normalizeSearchInput(input)
}

type searchRequest struct {
	Query           string   `json:"query"`
	SearchDepth     string   `json:"search_depth"`
	ChunksPerSource int      `json:"chunks_per_source,omitempty"`
	MaxResults      int      `json:"max_results"`
	Topic           string   `json:"topic,omitempty"`
	TimeRange       string   `json:"time_range,omitempty"`
	IncludeDomains  []string `json:"include_domains,omitempty"`
	Language        string   `json:"language,omitempty"`
	FilterLanguage  bool     `json:"filter_by_language,omitempty"`
	SafeSearch      bool     `json:"safe_search,omitempty"`
	IncludeUsage    bool     `json:"include_usage,omitempty"`
}

// SearchNews performs a bounded one-day news search for background watches.
func (t *Tool) SearchNews(ctx context.Context, query string) ([]Result, error) {
	query = strings.TrimSpace(query)
	if err := validateQuery(query); err != nil {
		return nil, err
	}
	started := time.Now()
	response, err := t.search(ctx, searchRequest{
		Query:          query,
		SearchDepth:    "basic",
		MaxResults:     quickMaxResults,
		Topic:          "news",
		TimeRange:      "day",
		Language:       "en",
		FilterLanguage: true,
		SafeSearch:     true,
		IncludeUsage:   true,
	})
	logErr := err
	if logErr == nil && len(response.Results) == 0 {
		logErr = ErrNoResults
	}
	t.logSearch(ctx, searchInput{
		Mode:    "quick",
		Topic:   "news",
		Recency: "day",
	}, time.Since(started), len(response.Results), response.Credits, logErr)
	return response.Results, err
}

func (t *Tool) logSearch(
	ctx context.Context,
	input searchInput,
	duration time.Duration,
	resultCount int,
	credits int,
	err error,
) {
	slog.InfoContext(ctx, "web search provider request",
		"provider", ProviderTavily,
		"mode", input.Mode,
		"topic", input.Topic,
		"recency", input.Recency,
		"domain_filter_count", len(input.IncludeDomains),
		"duration_ms", duration.Milliseconds(),
		"result_count", resultCount,
		"credits", credits,
		"outcome", searchErrorClass(err),
	)
}

func (t *Tool) search(ctx context.Context, parameters searchRequest) (searchResponse, error) {
	body, err := json.Marshal(parameters)
	if err != nil {
		return searchResponse{}, fmt.Errorf("encode Tavily search: %w", err)
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		t.endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return searchResponse{}, fmt.Errorf("create Tavily search: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+t.apiKey)
	request.Header.Set("Content-Type", "application/json")

	response, err := t.client.Do(request)
	if err != nil {
		return searchResponse{}, fmt.Errorf("send Tavily search: %w", err)
	}
	defer response.Body.Close()

	responseBody, tooLarge, err := readBounded(response.Body, maxResponseBodySize)
	if err != nil {
		return searchResponse{}, fmt.Errorf("read Tavily search: %w", err)
	}
	if tooLarge {
		return searchResponse{}, errors.New("Tavily search response exceeded size limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return searchResponse{}, fmt.Errorf("Tavily search returned status %d", response.StatusCode)
	}

	var payload struct {
		Results []struct {
			Title         string  `json:"title"`
			URL           string  `json:"url"`
			Content       string  `json:"content"`
			Score         float64 `json:"score"`
			PublishedDate string  `json:"published_date"`
		} `json:"results"`
		ResponseTime json.RawMessage `json:"response_time"`
		RequestID    string          `json:"request_id"`
		Usage        struct {
			Credits int `json:"credits"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return searchResponse{}, fmt.Errorf("decode Tavily search: %w", err)
	}
	if len(payload.Results) > parameters.MaxResults {
		payload.Results = payload.Results[:parameters.MaxResults]
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
			PublishedDate: truncate(item.PublishedDate, maxPublishedDateLength),
		})
	}
	return searchResponse{
		Results:      results,
		ResponseTime: normalizeResponseTime(payload.ResponseTime),
		RequestID:    strings.TrimSpace(payload.RequestID),
		Credits:      payload.Usage.Credits,
	}, nil
}

func normalizeResponseTime(value json.RawMessage) string {
	value = bytes.TrimSpace(value)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) {
		return ""
	}
	var text string
	if err := json.Unmarshal(value, &text); err == nil {
		return strings.TrimSpace(text)
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var number json.Number
	if err := decoder.Decode(&number); err == nil {
		return number.String()
	}
	return ""
}

func normalizeSearchInput(input searchInput) (searchInput, error) {
	input.Query = strings.TrimSpace(input.Query)
	if err := validateQuery(input.Query); err != nil {
		return searchInput{}, err
	}
	if input.Mode == "" {
		input.Mode = "quick"
	}
	if input.Mode != "quick" && input.Mode != "research" {
		return searchInput{}, errors.New("web search mode must be quick or research")
	}
	if input.Topic == "" {
		input.Topic = "general"
	}
	if input.Topic != "general" && input.Topic != "news" {
		return searchInput{}, errors.New("web search topic must be general or news")
	}
	if input.Recency == "" {
		input.Recency = "none"
	}
	if searchTimeRange(input.Recency) == "" && input.Recency != "none" {
		return searchInput{}, errors.New("web search recency is invalid")
	}
	if len(input.IncludeDomains) > maxDomainFilters {
		return searchInput{}, fmt.Errorf("web search accepts at most %d domains", maxDomainFilters)
	}

	domains := make([]string, 0, len(input.IncludeDomains))
	seen := make(map[string]struct{}, len(input.IncludeDomains))
	for _, domain := range input.IncludeDomains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		domain = strings.TrimPrefix(domain, "www.")
		if !domainPattern.MatchString(domain) {
			return searchInput{}, fmt.Errorf("invalid web search domain %q", domain)
		}
		if _, found := seen[domain]; found {
			continue
		}
		seen[domain] = struct{}{}
		domains = append(domains, domain)
	}
	input.IncludeDomains = domains
	return input, nil
}

func searchTimeRange(recency string) string {
	switch recency {
	case "day", "week", "month", "year":
		return recency
	default:
		return ""
	}
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
	if limit <= 0 {
		return ""
	}
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}
