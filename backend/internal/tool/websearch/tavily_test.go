package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const generalSearchArguments = `{"query":"test","mode":"quick","topic":"general","recency":"none","include_domains":[]}`

func TestToolSearchesTavily(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}

		var request searchRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request.Query != "ramen near 47.61,-122.34" ||
			request.SearchDepth != "advanced" ||
			request.ChunksPerSource != 3 ||
			request.MaxResults != researchMaxResults ||
			request.Topic != "general" ||
			request.TimeRange != "" ||
			len(request.IncludeDomains) != 1 ||
			request.IncludeDomains[0] != "example.com" ||
			request.Language != "en" ||
			!request.FilterLanguage ||
			!request.SafeSearch ||
			!request.IncludeUsage {
			t.Errorf("request = %#v", request)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"response_time": 1.25,
			"request_id":    "request-123",
			"usage":         map[string]any{"credits": 2},
			"results": []map[string]any{{
				"title":          " Ramen House ",
				"url":            " https://example.com/ramen ",
				"content":        " Open late with vegetarian options. ",
				"score":          0.92,
				"published_date": "2026-07-21T12:00:00Z",
			}},
		})
	}))
	defer server.Close()

	search, err := New(" test-key ")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	search.client = server.Client()
	search.endpoint = server.URL

	toolResult, err := search.Execute(
		context.Background(),
		tool.Scope{},
		json.RawMessage(`{"query":" ramen near 47.61,-122.34 ","mode":"research","topic":"general","recency":"none","include_domains":[" www.example.com "]}`),
	)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var response searchResponse
	if err := json.Unmarshal([]byte(toolResult.Content), &response); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(response.Results) != 1 ||
		response.Results[0].Title != "Ramen House" ||
		response.Results[0].URL != "https://example.com/ramen" ||
		response.Results[0].Snippet != "Open late with vegetarian options." ||
		response.Results[0].Score != 0.92 ||
		response.Results[0].PublishedDate != "2026-07-21T12:00:00Z" {
		t.Fatalf("result = %#v", response.Results)
	}
	if response.Query != "ramen near 47.61,-122.34" ||
		response.Mode != "research" ||
		response.Topic != "general" ||
		response.Recency != "none" ||
		response.Provider != ProviderTavily ||
		response.LatencyMS < 0 ||
		response.ResponseTime != "1.25" ||
		response.RequestID != "request-123" ||
		response.Credits != 2 {
		t.Fatalf("response metadata = %#v", response)
	}
	if search.Spec().Name != "search_web" || !search.Spec().ReadOnly {
		t.Fatalf("spec = %#v", search.Spec())
	}
}

func TestSearchNewsUsesExplicitBackgroundOptions(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request searchRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request.Query != "Did Nintendo announce its next console?" ||
			request.SearchDepth != "basic" ||
			request.ChunksPerSource != 0 ||
			request.Topic != "news" ||
			request.TimeRange != "day" ||
			request.MaxResults != quickMaxResults ||
			request.Language != "en" ||
			!request.FilterLanguage ||
			!request.SafeSearch ||
			!request.IncludeUsage {
			t.Errorf("request = %#v", request)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	}))
	defer server.Close()

	search, _ := New("test-key")
	search.client = server.Client()
	search.endpoint = server.URL
	results, err := search.SearchNews(context.Background(), " Did Nintendo announce its next console? ")
	if err != nil || len(results) != 0 {
		t.Fatalf("SearchNews() = %#v, %v", results, err)
	}
}

func TestToolBoundsSearchResults(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		results := make([]map[string]any, researchMaxResults+1)
		for index := range results {
			results[index] = map[string]any{
				"title":   "Result",
				"url":     "https://example.com",
				"content": strings.Repeat("a", maxSnippetLength+1),
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": results,
		})
	}))
	defer server.Close()

	search, _ := New("test-key")
	search.client = server.Client()
	search.endpoint = server.URL
	toolResult, err := search.Execute(
		context.Background(),
		tool.Scope{},
		json.RawMessage(`{"query":"test","mode":"research","topic":"general","recency":"none","include_domains":[]}`),
	)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var response struct {
		Results []Result `json:"results"`
	}
	if err := json.Unmarshal([]byte(toolResult.Content), &response); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(response.Results) != researchMaxResults {
		t.Fatalf("result count = %d", len(response.Results))
	}
	if response.Results[0].Snippet != strings.Repeat("a", maxSnippetLength-1)+"…" {
		t.Fatalf("snippet was not truncated: %s", response.Results[0].Snippet)
	}
}

func TestToolRejectsInvalidArguments(t *testing.T) {
	t.Parallel()

	search, _ := New("test-key")
	for _, arguments := range []string{
		`{"query":"","mode":"quick","topic":"general","recency":"none","include_domains":[]}`,
		`{"query":"test","mode":"deep","topic":"general","recency":"none","include_domains":[]}`,
		`{"query":"test","mode":"quick","topic":"sports","recency":"none","include_domains":[]}`,
		`{"query":"test","mode":"quick","topic":"general","recency":"hour","include_domains":[]}`,
		`{"query":"test","mode":"quick","topic":"general","recency":"none","include_domains":["https://example.com/path"]}`,
		`{"query":"test","mode":"quick","topic":"general","recency":"none","include_domains":["a.com","b.com","c.com","d.com","e.com","f.com"]}`,
		`{"query":"test","mode":"quick","topic":"general","recency":"none","include_domains":[],"extra":true}`,
		generalSearchArguments + ` {}`,
		`{"query":"` + strings.Repeat("a", maxQueryLength+1) + `","mode":"quick","topic":"general","recency":"none","include_domains":[]}`,
	} {
		if _, err := search.Execute(
			context.Background(),
			tool.Scope{},
			json.RawMessage(arguments),
		); err == nil {
			t.Fatalf("Execute(%s) error = nil", arguments)
		}
	}
}

func TestToolReportsTavilyFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	search, _ := New("test-key")
	search.client = server.Client()
	search.endpoint = server.URL
	_, err := search.Execute(
		context.Background(),
		tool.Scope{},
		json.RawMessage(generalSearchArguments),
	)
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestToolReportsEmptyResults(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	}))
	defer server.Close()

	search, _ := New("test-key")
	search.client = server.Client()
	search.endpoint = server.URL
	_, err := search.Execute(
		context.Background(),
		tool.Scope{},
		json.RawMessage(generalSearchArguments),
	)
	if !errors.Is(err, ErrNoResults) {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestNewRequiresAPIKey(t *testing.T) {
	t.Parallel()

	if _, err := New(" "); !errors.Is(err, ErrAPIKeyRequired) {
		t.Fatalf("New() error = %v", err)
	}
}

func TestTavilyLogsMetricsWithoutQueryText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"usage": map[string]any{"credits": 2},
			"results": []map[string]any{{
				"title": "Result", "url": "https://example.com", "content": "summary", "score": 1,
			}},
		})
	}))
	defer server.Close()
	search, _ := New("test-key")
	search.client = server.Client()
	search.endpoint = server.URL

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	secretQuery := "private purchase decision 739184"
	_, err := search.Execute(context.Background(), tool.Scope{}, json.RawMessage(
		`{"query":"`+secretQuery+`","mode":"research","topic":"general","recency":"none","include_domains":[]}`,
	))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := logs.String()
	if strings.Contains(output, secretQuery) || strings.Contains(output, "739184") {
		t.Fatalf("logs contained query: %s", output)
	}
	for _, field := range []string{
		"provider=tavily", "mode=research", "result_count=1", "credits=2", "outcome=none",
	} {
		if !strings.Contains(output, field) {
			t.Errorf("logs missing %q: %s", field, output)
		}
	}
}
