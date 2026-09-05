package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func TestNewConfiguredSelectsProvider(t *testing.T) {
	t.Parallel()

	defaultSearch, err := NewConfigured(Config{TavilyAPIKey: "key"})
	if err != nil {
		t.Fatalf("NewConfigured(default) error = %v", err)
	}
	if _, ok := defaultSearch.(*Tool); !ok {
		t.Fatalf("NewConfigured(default) type = %T", defaultSearch)
	}

	searxSearch, err := NewConfigured(Config{Provider: " SEARXNG "})
	if err != nil {
		t.Fatalf("NewConfigured(searxng) error = %v", err)
	}
	searx, ok := searxSearch.(*searxNGTool)
	if !ok {
		t.Fatalf("NewConfigured(searxng) type = %T", searxSearch)
	}
	if searx.endpoint != DefaultSearXNGBaseURL+"/search" {
		t.Fatalf("SearXNG endpoint = %q", searx.endpoint)
	}

	if _, err := NewConfigured(Config{Provider: "unknown"}); !errors.Is(err, ErrUnsupportedProvider) {
		t.Fatalf("NewConfigured(unknown) error = %v", err)
	}
	if _, err := NewConfigured(Config{Provider: ProviderSearXNG, SearXNGBaseURL: "file:///tmp/search"}); !errors.Is(err, ErrInvalidSearXNGBaseURL) {
		t.Fatalf("NewConfigured(invalid URL) error = %v", err)
	}
	if _, err := NewConfigured(Config{Provider: ProviderSearXNG, TavilyFallback: true}); !errors.Is(err, ErrAPIKeyRequired) {
		t.Fatalf("NewConfigured(fallback without key) error = %v", err)
	}
}

func TestSearXNGQuickSearchNormalizesAndBoundsResults(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm() error = %v", err)
		}
		wantForm := map[string]string{
			"q": "weekly weather outlook", "format": "json", "language": "en",
			"safesearch": "2", "categories": "general", "time_range": "week",
		}
		for key, want := range wantForm {
			if got := r.Form.Get(key); got != want {
				t.Errorf("form[%q] = %q, want %q", key, got, want)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"unresponsive_engines": []any{[]string{"brave", "timeout"}},
			"results": []map[string]any{
				{"title": "invalid", "url": "javascript:alert(1)", "content": "bad", "score": 99},
				{"title": " Best result ", "url": "https://example.com/best#section", "content": " useful summary ", "score": 4, "publishedDate": "2026-09-01T00:00:00Z", "engines": []string{"google"}},
				{"title": "Second", "url": "https://example.org/2", "content": "two", "score": 2},
				{"title": "Third", "url": "https://example.org/3", "content": "three", "score": 1},
				{"title": "Fourth", "url": "https://example.org/4", "content": "four", "score": .5},
				{"title": "Fifth", "url": "https://example.org/5", "content": "five", "score": .25},
				{"title": "Sixth", "url": "https://example.org/6", "content": "six", "score": .1},
			},
		})
	}))
	defer server.Close()

	searcher, err := newSearXNG(server.URL, false)
	if err != nil {
		t.Fatalf("newSearXNG() error = %v", err)
	}
	searcher.client = server.Client()
	result, err := searcher.Execute(context.Background(), tool.Scope{}, json.RawMessage(
		`{"query":"weekly weather outlook","mode":"quick","topic":"general","recency":"week","include_domains":[]}`,
	))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var response searchResponse
	if err := json.Unmarshal([]byte(result.Content), &response); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if len(response.Results) != quickMaxResults {
		t.Fatalf("result count = %d", len(response.Results))
	}
	first := response.Results[0]
	if first.Title != "Best result" || first.URL != "https://example.com/best" ||
		first.Snippet != "useful summary" || first.Score != 1 ||
		first.PublishedDate != "2026-09-01T00:00:00Z" {
		t.Fatalf("first result = %#v", first)
	}
	if response.Provider != ProviderSearXNG || response.UnresponsiveEngines != 1 || response.ResponsiveEngines != 1 {
		t.Fatalf("metadata = %#v", response)
	}
}

func TestSearXNGDomainSearchMergesDeduplicatesAndEnforcesHosts(t *testing.T) {
	t.Parallel()

	var lock sync.Mutex
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		query := r.Form.Get("q")
		lock.Lock()
		queries = append(queries, query)
		lock.Unlock()
		results := []map[string]any{{
			"title": "Rejected lookalike", "url": "https://example.com.attacker.test/result", "score": 10,
		}}
		if strings.Contains(query, "site:example.com") {
			results = append(results,
				map[string]any{"title": "Example", "url": "https://docs.example.com/result", "score": 2},
				map[string]any{"title": "Duplicate", "url": "https://docs.example.com/result#duplicate", "score": 1},
			)
		} else {
			results = append(results, map[string]any{"title": "Government", "url": "https://agency.gov/report", "score": 3})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
	}))
	defer server.Close()

	searcher, _ := newSearXNG(server.URL, false)
	searcher.client = server.Client()
	result, err := searcher.Execute(context.Background(), tool.Scope{}, json.RawMessage(
		`{"query":"source report","mode":"research","topic":"general","recency":"none","include_domains":["example.com","agency.gov"]}`,
	))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var response searchResponse
	_ = json.Unmarshal([]byte(result.Content), &response)
	if len(response.Results) != 2 {
		t.Fatalf("results = %#v", response.Results)
	}
	for _, result := range response.Results {
		parsed, _ := url.Parse(result.URL)
		if !matchesAnyDomain(parsed.Hostname(), []string{"example.com", "agency.gov"}) {
			t.Fatalf("off-domain result = %#v", result)
		}
	}
	lock.Lock()
	sort.Strings(queries)
	gotQueries := append([]string(nil), queries...)
	lock.Unlock()
	wantQueries := []string{"source report site:agency.gov", "source report site:example.com"}
	if strings.Join(gotQueries, "|") != strings.Join(wantQueries, "|") {
		t.Fatalf("queries = %#v", gotQueries)
	}
}

func TestSearXNGNewsAndEngineFailure(t *testing.T) {
	t.Parallel()

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		_ = r.ParseForm()
		if r.Form.Get("categories") != "news" || r.Form.Get("time_range") != "day" {
			t.Errorf("news form = %v", r.Form)
		}
		if requestCount == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{
				"title": "News", "url": "https://news.example/story", "content": "Update", "score": 1,
			}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results":              []any{},
			"unresponsive_engines": []any{[]string{"google news", "CAPTCHA"}},
		})
	}))
	defer server.Close()

	searcher, _ := newSearXNG(server.URL, false)
	searcher.client = server.Client()
	results, err := searcher.SearchNews(context.Background(), "launch announcement")
	if err != nil || len(results) != 1 {
		t.Fatalf("SearchNews() = %#v, %v", results, err)
	}
	_, err = searcher.SearchNews(context.Background(), "launch announcement")
	if !errors.Is(err, ErrSearXNGUnhealthy) {
		t.Fatalf("SearchNews(unhealthy) error = %v", err)
	}
}

func TestSearXNGNewsTreatsHealthyEmptyResponseAsSuccessfulCheck(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	}))
	defer server.Close()

	searcher, _ := newSearXNG(server.URL, false)
	searcher.client = server.Client()
	results, err := searcher.SearchNews(context.Background(), "quiet topic")
	if err != nil || len(results) != 0 {
		t.Fatalf("SearchNews() = %#v, %v, want healthy empty result", results, err)
	}
}

func TestSearXNGResearchExtractionUsesQueryAwareHTML(t *testing.T) {
	t.Parallel()

	pageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<html><body><script>secret noise</script>`+
			`<p>This generic introduction is deliberately long enough to become one extraction candidate for the page.</p>`+
			`<p>The Aurora telescope includes adaptive optics and a forty inch primary mirror for detailed observations.</p>`+
			`</body></html>`)
	}))
	defer pageServer.Close()

	searchServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{
			"title": "Telescope", "url": pageServer.URL, "content": "discovery snippet", "score": 1,
		}}})
	}))
	defer searchServer.Close()

	searcher, _ := newSearXNG(searchServer.URL, true)
	searcher.client = searchServer.Client()
	searcher.extractor = &pageExtractor{
		client:      pageServer.Client(),
		validateURL: func(context.Context, *url.URL) error { return nil },
	}
	result, err := searcher.Execute(context.Background(), tool.Scope{}, json.RawMessage(
		`{"query":"Aurora telescope adaptive optics","mode":"research","topic":"general","recency":"none","include_domains":[]}`,
	))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var response searchResponse
	_ = json.Unmarshal([]byte(result.Content), &response)
	if response.ExtractedResults != 1 || len(response.Results) != 1 {
		t.Fatalf("response = %#v", response)
	}
	snippet := strings.Join(response.Results[0].PageExcerpts, "\n\n")
	if !strings.Contains(snippet, "Aurora telescope") || strings.Contains(snippet, "secret noise") {
		t.Fatalf("extracted snippet = %q", snippet)
	}
}

func TestPageExtractionRejectsUnsafeDestinationsAndContent(t *testing.T) {
	t.Parallel()

	for _, address := range []string{
		"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.169.254",
		"100.64.0.1", "198.18.0.1", "::1", "fe80::1", "fd00::1",
	} {
		if isPublicIP(net.ParseIP(address)) {
			t.Errorf("isPublicIP(%q) = true", address)
		}
	}
	if !isPublicIP(net.ParseIP("93.184.216.34")) || !isPublicIP(net.ParseIP("2606:4700:4700::1111")) {
		t.Fatal("public addresses were rejected")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = io.WriteString(w, "not html")
	}))
	defer server.Close()
	extractor := &pageExtractor{
		client:      server.Client(),
		validateURL: func(context.Context, *url.URL) error { return nil },
	}
	if _, err := extractor.fetch(context.Background(), server.URL); err == nil || !strings.Contains(err.Error(), "not HTML") {
		t.Fatalf("fetch(non-HTML) error = %v", err)
	}

	secureExtractor := newPageExtractor()
	if _, err := secureExtractor.fetch(context.Background(), "http://169.254.169.254/latest/meta-data"); err == nil {
		t.Fatal("fetch(metadata) error = nil")
	}
}

func TestResolvePublicIPsRejectsMixedDNSAnswers(t *testing.T) {
	t.Parallel()

	resolver := staticIPResolver{addresses: []net.IPAddr{
		{IP: net.ParseIP("93.184.216.34")},
		{IP: net.ParseIP("10.0.0.8")},
	}}
	if _, err := resolvePublicIPs(context.Background(), resolver, "mixed.example"); err == nil {
		t.Fatal("resolvePublicIPs(mixed) error = nil")
	}
	resolver.addresses = []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}
	addresses, err := resolvePublicIPs(context.Background(), resolver, "public.example")
	if err != nil || len(addresses) != 1 {
		t.Fatalf("resolvePublicIPs(public) = %#v, %v", addresses, err)
	}
}

func TestPageExtractionValidatesRedirects(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/blocked", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<p>This redirect target must not be fetched.</p>")
	}))
	defer server.Close()

	var validations atomic.Int32
	extractor := &pageExtractor{}
	extractor.validateURL = func(_ context.Context, target *url.URL) error {
		validations.Add(1)
		if target.Path == "/blocked" {
			return errors.New("blocked redirect")
		}
		return nil
	}
	extractor.client = &http.Client{
		Transport: server.Client().Transport,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			return extractor.validateURL(request.Context(), request.URL)
		},
	}
	if _, err := extractor.fetch(context.Background(), server.URL+"/start"); err == nil {
		t.Fatal("fetch(redirect) error = nil")
	}
	if validations.Load() != 2 {
		t.Fatalf("redirect validations = %d", validations.Load())
	}
}

func TestReadBoundedDetectsOverflow(t *testing.T) {
	t.Parallel()

	content, tooLarge, err := readBounded(strings.NewReader("12345"), 5)
	if err != nil || tooLarge || string(content) != "12345" {
		t.Fatalf("readBounded(exact) = %q, %t, %v", content, tooLarge, err)
	}
	content, tooLarge, err = readBounded(strings.NewReader("123456"), 5)
	if err != nil || !tooLarge || content != nil {
		t.Fatalf("readBounded(overflow) = %q, %t, %v", content, tooLarge, err)
	}
}

func TestPageExtractionBoundsConcurrency(t *testing.T) {
	t.Parallel()

	var active atomic.Int32
	var maximum atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<p>This page contains enough searchable telescope information for a useful bounded chunk.</p>")
	}))
	defer server.Close()
	extractor := &pageExtractor{
		client:      server.Client(),
		validateURL: func(context.Context, *url.URL) error { return nil },
	}
	results := make([]Result, researchMaxResults)
	for index := range results {
		results[index] = Result{URL: server.URL + "/page" + string(rune('a'+index))}
	}
	_, extracted := extractor.enrich(context.Background(), "telescope information", results)
	if extracted != researchMaxResults {
		t.Fatalf("extracted = %d", extracted)
	}
	if maximum.Load() > extractionConcurrency {
		t.Fatalf("maximum concurrency = %d", maximum.Load())
	}
}

func TestSearXNGLogsDoNotContainQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	searcher, _ := newSearXNG(server.URL, false)
	searcher.client = server.Client()

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	secretQuery := "private medical appointment 847291"
	_, _ = searcher.SearchNews(context.Background(), secretQuery)
	if strings.Contains(logs.String(), secretQuery) || strings.Contains(logs.String(), "847291") {
		t.Fatalf("logs contained query: %s", logs.String())
	}
}

func TestFallbackSearcherOnlyFallsBackForProviderFailures(t *testing.T) {
	t.Parallel()

	var fallbackCalls atomic.Int32
	primary := &stubSearcher{
		execute: func(context.Context, tool.Scope, json.RawMessage) (tool.Result, error) {
			return tool.Result{}, ErrNoResults
		},
		news: func(context.Context, string) ([]Result, error) { return nil, ErrSearXNGUnhealthy },
	}
	fallback := &stubSearcher{
		execute: func(context.Context, tool.Scope, json.RawMessage) (tool.Result, error) {
			fallbackCalls.Add(1)
			return tool.Result{Content: "fallback"}, nil
		},
		news: func(context.Context, string) ([]Result, error) {
			fallbackCalls.Add(1)
			return []Result{{Title: "fallback"}}, nil
		},
	}
	searcher := &fallbackSearcher{primary: primary, fallback: fallback}
	result, err := searcher.Execute(context.Background(), tool.Scope{}, json.RawMessage(generalSearchArguments))
	if err != nil || result.Content != "fallback" {
		t.Fatalf("Execute() = %#v, %v", result, err)
	}
	results, err := searcher.SearchNews(context.Background(), "news")
	if err != nil || len(results) != 1 {
		t.Fatalf("SearchNews() = %#v, %v", results, err)
	}
	if fallbackCalls.Load() != 2 {
		t.Fatalf("fallback calls = %d", fallbackCalls.Load())
	}
	primary.news = func(context.Context, string) ([]Result, error) { return nil, nil }
	results, err = searcher.SearchNews(context.Background(), "quiet news")
	if err != nil || len(results) != 0 {
		t.Fatalf("SearchNews(healthy empty) = %#v, %v", results, err)
	}
	if fallbackCalls.Load() != 2 {
		t.Fatalf("healthy empty result used fallback; calls = %d", fallbackCalls.Load())
	}
	if _, err := searcher.Execute(context.Background(), tool.Scope{}, json.RawMessage(`{"query":""}`)); err == nil {
		t.Fatal("Execute(invalid) error = nil")
	}
	if _, err := searcher.SearchNews(context.Background(), "   "); err == nil {
		t.Fatal("SearchNews(invalid) error = nil")
	}
	if fallbackCalls.Load() != 2 {
		t.Fatalf("invalid arguments used fallback; calls = %d", fallbackCalls.Load())
	}
}

type stubSearcher struct {
	execute func(context.Context, tool.Scope, json.RawMessage) (tool.Result, error)
	news    func(context.Context, string) ([]Result, error)
}

type staticIPResolver struct {
	addresses []net.IPAddr
}

func (r staticIPResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return r.addresses, nil
}

func (s *stubSearcher) Spec() tool.Spec { return tool.Spec{Name: "search_web"} }

func (s *stubSearcher) Execute(ctx context.Context, scope tool.Scope, arguments json.RawMessage) (tool.Result, error) {
	return s.execute(ctx, scope, arguments)
}

func (s *stubSearcher) SearchNews(ctx context.Context, query string) ([]Result, error) {
	return s.news(ctx, query)
}
