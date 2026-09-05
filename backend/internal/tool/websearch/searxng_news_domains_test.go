package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func TestSearXNGNewsDomainLookupConsultsBothIndexesAndKeepsStrictHosts(t *testing.T) {
	t.Parallel()
	for _, recency := range []string{"day", "week"} {
		t.Run(recency, func(t *testing.T) {
			var mu sync.Mutex
			var forms []url.Values
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/search" {
					t.Errorf("request = %s %s, want POST /search", r.Method, r.URL.Path)
				}
				if err := r.ParseForm(); err != nil {
					t.Errorf("parse form: %v", err)
				}
				mu.Lock()
				forms = append(forms, r.PostForm)
				mu.Unlock()
				_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{
					{"title": "Official telescope announcement", "url": "https://aurora.example/news/announcement", "content": "Aurora telescope launch confirmed.", "score": 4, "engines": []string{"google"}},
					{"title": "Official observatory notice", "url": "https://news.observatory.example/launch", "content": "Launch confirmation and observing details.", "score": 3, "engines": []string{"yahoo news"}},
					{"title": "Deceptive suffix", "url": "https://aurora.example.attacker.test/rumor", "score": 100},
					{"title": "Deceptive prefix", "url": "https://notaurora.example/rumor", "score": 100},
					{"title": "Credentials deception", "url": "https://aurora.example@attacker.test/rumor", "score": 100},
					{"title": "Unrequested third party", "url": "https://elsewhere.example/rumor", "score": 100},
				}})
			}))
			defer server.Close()
			searcher, err := newSearXNG(server.URL, false)
			if err != nil {
				t.Fatal(err)
			}
			searcher.client = server.Client()
			arguments, _ := json.Marshal(searchInput{Query: "Aurora telescope launch confirmation", Mode: "research", Topic: "news", Recency: recency, IncludeDomains: []string{"aurora.example", "observatory.example"}})
			result, err := searcher.Execute(context.Background(), tool.Scope{}, arguments)
			if err != nil {
				t.Fatal(err)
			}
			var response searchResponse
			if err := json.Unmarshal([]byte(result.Content), &response); err != nil {
				t.Fatal(err)
			}
			if response.Topic != "news" || response.Recency != recency || response.Provider != ProviderSearXNG {
				t.Errorf("public response changed topic/provider/recency: %#v", response)
			}
			if len(response.Results) != 2 {
				t.Fatalf("result count = %d, want only two unique allowed-domain results", len(response.Results))
			}
			for _, item := range response.Results {
				parsed, err := url.Parse(item.URL)
				if err != nil || (parsed.Hostname() != "aurora.example" && parsed.Hostname() != "news.observatory.example") {
					t.Errorf("off-domain result leaked: %q", item.URL)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if len(forms) != 2 {
				t.Fatalf("request count=%d, want one bounded dual-index request per domain", len(forms))
			}
			seenQueries := make(map[string]bool)
			for _, form := range forms {
				if form.Get("categories") != "news,general" || form.Get("time_range") != recency {
					t.Errorf("dual-index request lost category or recency: %v", form)
				}
				seenQueries[form.Get("q")] = true
			}
			for _, domain := range []string{"aurora.example", "observatory.example"} {
				if !seenQueries["Aurora telescope launch confirmation site:"+domain] {
					t.Errorf("missing exact scoped query for %s: %v", domain, seenQueries)
				}
			}
		})
	}
}

func TestSearXNGNewsDomainLookupDoesNotRelaxEmptyAllowedDomainResults(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{
			"title": "Wrong publisher", "url": "https://aurora.example.attacker.test/article", "content": "Unverified launch rumor", "score": 99,
		}}})
	}))
	defer server.Close()
	searcher, err := newSearXNG(server.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	searcher.client = server.Client()
	_, err = searcher.Execute(context.Background(), tool.Scope{}, json.RawMessage(`{"query":"Aurora telescope launch","mode":"quick","topic":"news","recency":"week","include_domains":["aurora.example"]}`))
	if !errors.Is(err, ErrNoResults) {
		t.Errorf("off-domain-only search error=%v, want ErrNoResults", err)
	}
}

func TestSearXNGNewsOnlyBackgroundSearchRemainsOneDayNewsIndex(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if r.Form.Get("categories") != "news" || r.Form.Get("time_range") != "day" ||
			r.Form.Get("q") != "Aurora telescope launch" || strings.Contains(r.Form.Get("q"), "site:") {
			t.Errorf("background news semantics changed: %v", r.Form)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"title": "Confirmed launch", "url": "https://aurora.example/launch", "content": "Aurora launch confirmed", "score": 1}}})
	}))
	defer server.Close()
	searcher, err := newSearXNG(server.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	searcher.client = server.Client()
	results, err := searcher.SearchNews(context.Background(), "Aurora telescope launch")
	if err != nil || len(results) != 1 {
		t.Errorf("SearchNews() results=%#v error=%v", results, err)
	}
}

func TestSearXNGIndexWideningIsLimitedToNewsWithDomains(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name, topic, category string
		domains               []string
	}{
		{"unscoped_news", "news", "news", []string{}},
		{"scoped_general", "general", "general", []string{"aurora.example"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				if r.Form.Get("categories") != scenario.category || r.Form.Get("time_range") != "week" {
					t.Errorf("index widening escaped intended scope: %v", r.Form)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"title": "Aurora launch", "url": "https://aurora.example/launch", "content": "Confirmed launch", "score": 1}}})
			}))
			defer server.Close()
			searcher, err := newSearXNG(server.URL, false)
			if err != nil {
				t.Fatal(err)
			}
			searcher.client = server.Client()
			arguments, _ := json.Marshal(searchInput{Query: "Aurora telescope launch", Mode: "quick", Topic: scenario.topic, Recency: "week", IncludeDomains: scenario.domains})
			if _, err := searcher.Execute(context.Background(), tool.Scope{}, arguments); err != nil {
				t.Fatal(err)
			}
		})
	}
}
