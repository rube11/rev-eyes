package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const generalSearchArguments = `{"query":"test"}`

func TestToolSearchesTavily(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}

		var request struct {
			Query          string `json:"query"`
			AutoParameters bool   `json:"auto_parameters"`
			SearchDepth    string `json:"search_depth"`
			MaxResults     int    `json:"max_results"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request.Query != "ramen near 47.61,-122.34" ||
			!request.AutoParameters ||
			request.SearchDepth != "fast" ||
			request.MaxResults != maxResults {
			t.Errorf("request = %#v", request)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
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
		json.RawMessage(`{"query":" ramen near 47.61,-122.34 "}`),
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
	if len(response.Results) != 1 ||
		response.Results[0].Title != "Ramen House" ||
		response.Results[0].URL != "https://example.com/ramen" ||
		response.Results[0].Snippet != "Open late with vegetarian options." ||
		response.Results[0].Score != 0.92 ||
		response.Results[0].PublishedDate != "2026-07-21T12:00:00Z" {
		t.Fatalf("result = %#v", response.Results)
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
			request.AutoParameters ||
			request.SearchDepth != "basic" ||
			request.Topic != "news" ||
			request.TimeRange != "day" ||
			request.MaxResults != maxResults {
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
		results := make([]map[string]any, maxResults+1)
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
		json.RawMessage(generalSearchArguments),
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
	if len(response.Results) != maxResults {
		t.Fatalf("result count = %d", len(response.Results))
	}
	if response.Results[0].Snippet != strings.Repeat("a", maxSnippetLength)+"…" {
		t.Fatalf("snippet was not truncated: %s", response.Results[0].Snippet)
	}
}

func TestToolRejectsInvalidArguments(t *testing.T) {
	t.Parallel()

	search, _ := New("test-key")
	for _, arguments := range []string{
		`{"query":""}`,
		`{"query":"test","extra":true}`,
		generalSearchArguments + ` {}`,
		`{"query":"` + strings.Repeat("a", maxQueryLength+1) + `"}`,
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
