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
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const (
	searxSearchTimeout      = 8 * time.Second
	searxTotalTimeout       = 20 * time.Second
	searxDomainConcurrency  = 3
	searxMaxResultsPerQuery = 25
)

var (
	ErrSearXNGBaseURLRequired = errors.New("SEARXNG_BASE_URL is required")
	ErrInvalidSearXNGBaseURL  = errors.New("SEARXNG_BASE_URL must be an http or https URL without credentials")
	ErrSearXNGUnhealthy       = errors.New("SearXNG returned no results and reported unresponsive engines")
	searxResearchParameters   = researchOnlySearchParameters()
)

// Derive only the model-facing mode enum and description from the shared schema.
// A malformed declaration is a programming error, not a runtime configuration.
func researchOnlySearchParameters() string {
	var schema map[string]any
	if err := json.Unmarshal([]byte(parametersSchema), &schema); err != nil {
		panic("invalid static web search schema: " + err.Error())
	}
	mode := schema["properties"].(map[string]any)["mode"].(map[string]any)
	mode["enum"] = []string{"research"}
	mode["description"] = "Use research to discover sources and fetch pages for verification."
	encoded, err := json.Marshal(schema)
	if err != nil {
		panic("encode static research search schema: " + err.Error())
	}
	return string(encoded)
}

// searxNGTool preserves the public search_web contract while sourcing discovery
// results from a private SearXNG service.
type searxNGTool struct {
	client             *http.Client
	endpoint           string
	researchExtraction bool
	extractor          *pageExtractor
}

type searxResult struct {
	Title         string          `json:"title"`
	URL           string          `json:"url"`
	Content       string          `json:"content"`
	Score         float64         `json:"score"`
	PublishedDate json.RawMessage `json:"publishedDate"`
	PublishedAlt  json.RawMessage `json:"published_date"`
	Engines       []string        `json:"engines"`
}

type searxResponse struct {
	Results             []searxResult     `json:"results"`
	UnresponsiveEngines []json.RawMessage `json:"unresponsive_engines"`
}

type searxOutcome struct {
	results      []searxResult
	unresponsive int
	engines      map[string]struct{}
	err          error
}

func newSearXNG(baseURL string, researchExtraction bool) (*searxNGTool, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, ErrSearXNGBaseURLRequired
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, ErrInvalidSearXNGBaseURL
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, ErrInvalidSearXNGBaseURL
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/search"
	parsed.RawPath = ""

	return &searxNGTool{
		client:             &http.Client{Timeout: searxSearchTimeout},
		endpoint:           parsed.String(),
		researchExtraction: researchExtraction,
		extractor:          newPageExtractor(),
	}, nil
}

func (t *searxNGTool) Spec() tool.Spec {
	parameters := parametersSchema
	if t.researchExtraction {
		// Normal strict model generation must request fetched evidence. This is
		// not server-side enforcement: direct Execute callers retain quick mode.
		parameters = searxResearchParameters
	}
	return tool.Spec{
		Name:        "search_web",
		Description: "Search current public-web evidence using concise targeted queries. Discover candidates or verify a named candidate's missing facts; use domain filters only for user-specified or evidence-identified sites.",
		Parameters:  json.RawMessage(parameters),
		ReadOnly:    true,
	}
}

func (t *searxNGTool) Execute(
	ctx context.Context,
	_ tool.Scope,
	arguments json.RawMessage,
) (tool.Result, error) {
	input, err := decodeSearchInput(arguments)
	if err != nil {
		return tool.Result{}, err
	}

	started := time.Now()
	searchCtx, cancel := context.WithTimeout(ctx, searxTotalTimeout)
	defer cancel()

	response, err := t.search(searchCtx, input)
	if err != nil {
		t.logSearch(ctx, input, time.Since(started), 0, response.UnresponsiveEngines, 0, err)
		return tool.Result{}, err
	}

	extracted := 0
	if input.Mode == "research" && t.researchExtraction && len(response.Results) > 0 {
		response.Results, extracted = t.extractor.enrich(searchCtx, input.Query, response.Results, input.IncludeDomains...)
	}
	response.Query = input.Query
	response.Mode = input.Mode
	response.Topic = input.Topic
	response.Recency = input.Recency
	response.Provider = ProviderSearXNG
	response.LatencyMS = time.Since(started).Milliseconds()
	response.ExtractedResults = extracted

	encoded, err := json.Marshal(response)
	if err != nil {
		return tool.Result{}, fmt.Errorf("encode web search results: %w", err)
	}
	t.logSearch(ctx, input, time.Since(started), len(response.Results), response.UnresponsiveEngines, extracted, nil)
	return tool.Result{Content: string(encoded)}, nil
}

// SearchNews performs a bounded one-day SearXNG news search for background watches.
func (t *searxNGTool) SearchNews(ctx context.Context, query string) ([]Result, error) {
	input := searchInput{
		Query:   strings.TrimSpace(query),
		Mode:    "quick",
		Topic:   "news",
		Recency: "day",
	}
	if err := validateQuery(input.Query); err != nil {
		return nil, err
	}

	started := time.Now()
	searchCtx, cancel := context.WithTimeout(ctx, searxTotalTimeout)
	defer cancel()
	response, err := t.search(searchCtx, input)
	if err != nil {
		if errors.Is(err, ErrNoResults) {
			t.logSearch(ctx, input, time.Since(started), 0, 0, 0, err)
			return []Result{}, nil
		}
		t.logSearch(ctx, input, time.Since(started), 0, response.UnresponsiveEngines, 0, err)
		return nil, err
	}
	t.logSearch(ctx, input, time.Since(started), len(response.Results), response.UnresponsiveEngines, 0, nil)
	return response.Results, nil
}

func (t *searxNGTool) search(ctx context.Context, input searchInput) (searchResponse, error) {
	category := input.Topic
	if category == "news" && len(input.IncludeDomains) > 0 {
		// Official transaction/announcement pages are often in the web index
		// rather than the news index. Retain the news time range and strict host
		// checks while consulting both indexes in the same bounded request.
		category = "news,general"
	}
	queries := make([]string, 0, max(1, len(input.IncludeDomains)))
	if len(input.IncludeDomains) == 0 {
		queries = append(queries, input.Query)
	} else {
		for _, domain := range input.IncludeDomains {
			queries = append(queries, input.Query+" site:"+domain)
		}
	}

	outcomes := make([]searxOutcome, len(queries))
	semaphore := make(chan struct{}, searxDomainConcurrency)
	var group sync.WaitGroup
	for index, query := range queries {
		group.Add(1)
		go func(index int, query string) {
			defer group.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				outcomes[index].err = ctx.Err()
				return
			}
			outcomes[index] = t.searchOnce(ctx, query, category, input.Recency)
		}(index, query)
	}
	group.Wait()

	maxResults := quickMaxResults
	if input.Mode == "research" {
		maxResults = researchMaxResults
	}
	merged := make([]searxResult, 0, maxResults)
	seenURLs := make(map[string]struct{}, maxResults)
	responsiveEngines := make(map[string]struct{})
	unresponsive := 0
	var firstErr error
	for _, outcome := range outcomes {
		unresponsive += outcome.unresponsive
		for engine := range outcome.engines {
			responsiveEngines[engine] = struct{}{}
		}
		if outcome.err != nil {
			if firstErr == nil {
				firstErr = outcome.err
			}
			continue
		}
		for _, item := range outcome.results {
			normalizedURL, hostname, ok := normalizePublicResultURL(item.URL)
			if !ok || strings.TrimSpace(item.Title) == "" ||
				(len(input.IncludeDomains) > 0 && !matchesAnyDomain(hostname, input.IncludeDomains)) {
				continue
			}
			if _, exists := seenURLs[normalizedURL]; exists {
				continue
			}
			item.URL = normalizedURL
			seenURLs[normalizedURL] = struct{}{}
			merged = append(merged, item)
		}
	}

	if len(merged) == 0 {
		metadata := searchResponse{
			UnresponsiveEngines: unresponsive,
			ResponsiveEngines:   len(responsiveEngines),
		}
		if firstErr != nil {
			return metadata, firstErr
		}
		if unresponsive > 0 {
			return metadata, ErrSearXNGUnhealthy
		}
		return metadata, ErrNoResults
	}

	results := normalizeSearXResults(merged, maxResults)
	if len(results) == 0 {
		return searchResponse{}, ErrNoResults
	}
	return searchResponse{
		Results:             results,
		UnresponsiveEngines: unresponsive,
		ResponsiveEngines:   len(responsiveEngines),
	}, nil
}

func (t *searxNGTool) searchOnce(
	ctx context.Context,
	query string,
	topic string,
	recency string,
) searxOutcome {
	form := url.Values{
		"q":          {query},
		"format":     {"json"},
		"language":   {"en"},
		"safesearch": {"2"},
	}
	if topic == "news" || topic == "news,general" {
		form.Set("categories", topic)
	} else {
		form.Set("categories", "general")
	}
	if timeRange := searchTimeRange(recency); timeRange != "" {
		form.Set("time_range", timeRange)
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		t.endpoint,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return searxOutcome{err: errors.New("create SearXNG search request failed")}
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")

	response, err := t.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return searxOutcome{err: ctx.Err()}
		}
		return searxOutcome{err: errors.New("send SearXNG search failed")}
	}
	defer response.Body.Close()

	responseBody, tooLarge, err := readBounded(response.Body, maxResponseBodySize)
	if err != nil {
		return searxOutcome{err: errors.New("read SearXNG search response failed")}
	}
	if tooLarge {
		return searxOutcome{err: errors.New("SearXNG search response exceeded size limit")}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return searxOutcome{err: fmt.Errorf("SearXNG search returned status %d", response.StatusCode)}
	}

	var payload searxResponse
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	if err := decoder.Decode(&payload); err != nil {
		return searxOutcome{err: errors.New("decode SearXNG search response failed")}
	}
	if len(payload.Results) > searxMaxResultsPerQuery {
		payload.Results = payload.Results[:searxMaxResultsPerQuery]
	}
	engines := make(map[string]struct{})
	for _, item := range payload.Results {
		for _, engine := range item.Engines {
			engine = strings.TrimSpace(engine)
			if engine != "" {
				engines[engine] = struct{}{}
			}
		}
	}
	return searxOutcome{
		results:      payload.Results,
		unresponsive: len(payload.UnresponsiveEngines),
		engines:      engines,
	}
}

func normalizeSearXResults(items []searxResult, limit int) []Result {
	sort.SliceStable(items, func(left, right int) bool {
		return items[left].Score > items[right].Score
	})
	if len(items) > limit {
		items = items[:limit]
	}
	maxScore := 0.0
	for _, item := range items {
		if item.Score > maxScore {
			maxScore = item.Score
		}
	}
	results := make([]Result, 0, len(items))
	for index, item := range items {
		title := strings.TrimSpace(item.Title)
		itemURL := strings.TrimSpace(item.URL)
		if title == "" || itemURL == "" {
			continue
		}
		score := item.Score
		if maxScore > 0 {
			score /= maxScore
		} else {
			score = 1 / float64(index+1)
		}
		if score < 0 {
			score = 0
		}
		if score > 1 {
			score = 1
		}
		results = append(results, Result{
			Title:            truncate(title, maxTitleLength),
			URL:              itemURL,
			Snippet:          truncate(item.Content, maxSnippetLength),
			DiscoverySnippet: truncate(item.Content, maxSnippetLength),
			ExtractionStatus: extractionNotRequested,
			Score:            score,
			PublishedDate: truncate(
				normalizePublishedDate(item.PublishedDate, item.PublishedAlt),
				maxPublishedDateLength,
			),
		})
	}
	sort.SliceStable(results, func(left, right int) bool {
		return results[left].Score > results[right].Score
	})
	return results
}

func normalizePublishedDate(values ...json.RawMessage) string {
	for _, value := range values {
		value = bytes.TrimSpace(value)
		if len(value) == 0 || bytes.Equal(value, []byte("null")) {
			continue
		}
		var text string
		if json.Unmarshal(value, &text) == nil {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func normalizePublicResultURL(rawURL string) (string, string, bool) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || len(rawURL) > maxURLLength {
		return "", "", false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", "", false
	}
	hostname := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if hostname == "" {
		return "", "", false
	}
	parsed.Fragment = ""
	return parsed.String(), hostname, true
}

func matchesAnyDomain(hostname string, domains []string) bool {
	for _, domain := range domains {
		if hostname == domain || strings.HasSuffix(hostname, "."+domain) {
			return true
		}
	}
	return false
}

func readBounded(reader io.Reader, limit int64) ([]byte, bool, error) {
	content, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(content)) > limit {
		return nil, true, nil
	}
	return content, false, nil
}

func (t *searxNGTool) logSearch(
	ctx context.Context,
	input searchInput,
	duration time.Duration,
	resultCount int,
	unresponsiveEngines int,
	extractedResults int,
	err error,
) {
	slog.InfoContext(ctx, "web search provider request",
		"provider", ProviderSearXNG,
		"mode", input.Mode,
		"topic", input.Topic,
		"recency", input.Recency,
		"domain_filter_count", len(input.IncludeDomains),
		"duration_ms", duration.Milliseconds(),
		"result_count", resultCount,
		"unresponsive_engine_count", unresponsiveEngines,
		"extracted_result_count", extractedResults,
		"outcome", searchErrorClass(err),
	)
}
