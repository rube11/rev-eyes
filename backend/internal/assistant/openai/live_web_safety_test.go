package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/tool"
	"github.com/rube11/rev-eyes/backend/internal/tool/websearch"
)

var errLiveTavilyBlocked = errors.New("Tavily requests are forbidden in web-research evaluations")
var errLiveUnnormalizedHost = errors.New("raw non-ASCII hostnames are forbidden in web-research evaluations; use a validated ASCII hostname")

type liveSearchSafetyConfiguration struct {
	Provider                     string `json:"provider"`
	SearXNGBaseURL               string `json:"searxng_base_url"`
	TavilyFallback               bool   `json:"tavily_fallback"`
	ResearchExtraction           bool   `json:"research_extraction"`
	TavilyProviderNetworkBlocked bool   `json:"tavily_provider_network_blocked"`
	TavilyGuardScope             string `json:"tavily_guard_scope"`
}

// No provider/fallback/key environment variables are consulted here. Keeping
// this policy in one helper makes both live runner entry points fail closed.
func noTavilyLiveSearchConfig(baseURL string) websearch.Config {
	return websearch.Config{
		Provider: websearch.ProviderSearXNG, SearXNGBaseURL: baseURL,
		TavilyAPIKey: "", TavilyFallback: false, ResearchExtraction: true,
	}
}

func newNoTavilyLiveSearcher(config websearch.Config) (websearch.Searcher, error) {
	if config.Provider != websearch.ProviderSearXNG || config.TavilyFallback || config.TavilyAPIKey != "" {
		return nil, errors.New("live web evaluation requires SearXNG with no Tavily key or fallback")
	}
	parsed, err := url.Parse(config.SearXNGBaseURL)
	if err != nil || parsed.Hostname() == "" || isTavilyHost(parsed.Hostname()) {
		return nil, errors.New("live web evaluation requires an explicit non-Tavily SearXNG URL")
	}
	if !isASCIIHost(parsed.Hostname()) {
		return nil, errLiveUnnormalizedHost
	}
	// The page extractor has its own SSRF-safe transport, so attach the same
	// tripwire before its DNS validation rather than claiming default-transport
	// coverage for a client that does not use it. Ignore caller-supplied policy.
	guard, _ := http.DefaultTransport.(*liveTavilyBlockTransport)
	config.ExtractionURLPolicy = func(target *url.URL) error {
		if target == nil {
			return nil
		}
		if !isASCIIHost(target.Hostname()) {
			if guard != nil {
				return guard.blockUnnormalizedHost(target.Hostname())
			}
			return errLiveUnnormalizedHost
		}
		if !isTavilyHost(target.Hostname()) {
			return nil
		}
		if guard != nil {
			return guard.blockHost(target.Hostname())
		}
		return errLiveTavilyBlocked
	}
	return websearch.NewConfigured(config)
}

// The Tavily adapter uses http.DefaultTransport. Intercept it before any live
// client is constructed; a fallback attempt fails before DNS, TLS, or a request
// body can leave the process. A denylist is used so OpenAI and SearXNG continue
// normally. The safe constructor attaches this tripwire to page extraction's
// pre-DNS policy as well; this is not a machine-wide firewall.
// Tests using this guard must remain serial (t.Setenv enforces that constraint).
func installLiveTavilyBlock(t *testing.T) *liveTavilyBlockTransport {
	t.Helper()
	t.Setenv("TAVILY_API_KEY", "")
	t.Setenv("WEB_SEARCH_TAVILY_FALLBACK", "false")
	previous := http.DefaultTransport
	guard := &liveTavilyBlockTransport{
		delegate: previous,
		onBlocked: func(host string) {
			t.Errorf("blocked forbidden Tavily network request to %s", host)
		},
		onUnnormalized: func(host string) {
			t.Errorf("blocked unnormalized non-ASCII destination hostname %q before network access", host)
		},
	}
	http.DefaultTransport = guard
	t.Cleanup(func() {
		http.DefaultTransport = previous
		if attempts := guard.attempts.Load(); attempts != 0 {
			t.Errorf("Tavily network guard recorded %d forbidden request(s)", attempts)
		}
		if attempts := guard.unnormalizedAttempts.Load(); attempts != 0 {
			t.Errorf("web evaluation guard recorded %d unnormalized-host request(s); these are not classified as Tavily", attempts)
		}
	})
	return guard
}

type liveTavilyBlockTransport struct {
	delegate             http.RoundTripper
	onBlocked            func(string)
	onUnnormalized       func(string)
	attempts             atomic.Int64
	unnormalizedAttempts atomic.Int64
}

func (g *liveTavilyBlockTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	hosts := []string{request.URL.Hostname(), request.Host}
	for _, host := range hosts {
		if hostOnly, _, err := net.SplitHostPort(host); err == nil {
			host = hostOnly
		}
		if isTavilyHost(host) {
			return nil, g.blockHost(host)
		}
		if !isASCIIHost(host) {
			return nil, g.blockUnnormalizedHost(host)
		}
	}
	return g.delegate.RoundTrip(request)
}

func (g *liveTavilyBlockTransport) blockHost(host string) error {
	g.attempts.Add(1)
	if g.onBlocked != nil {
		// Only the destination hostname is logged, never URL, body or key.
		g.onBlocked(strings.ToLower(strings.TrimRight(host, ".")))
	}
	return errLiveTavilyBlocked
}

func (g *liveTavilyBlockTransport) blockUnnormalizedHost(host string) error {
	g.unnormalizedAttempts.Add(1)
	if g.onUnnormalized != nil {
		g.onUnnormalized(host) // Host only, never URL path, query, body, or headers.
	}
	return errLiveUnnormalizedHost
}

// Go's transport applies IDNA lookup mappings before dialing. The evaluation
// guard does not have a matching IDNA dependency, so reject raw Unicode instead
// of implementing an incomplete normalization table. Normal production is not
// affected; ASCII/punycode public hosts remain eligible for research.
func isASCIIHost(host string) bool {
	for index := range host {
		if host[index] >= 0x80 {
			return false
		}
	}
	return true
}

func isTavilyHost(host string) bool {
	host = strings.ToLower(strings.TrimRight(host, "."))
	return host == "tavily.com" || strings.HasSuffix(host, ".tavily.com")
}

func assertNoTavilySearchResult(t *testing.T, content string) {
	t.Helper()
	if content == "" {
		return
	}
	var metadata struct {
		Provider string `json:"provider"`
		Credits  int    `json:"credits"`
	}
	if err := json.Unmarshal([]byte(content), &metadata); err != nil {
		t.Errorf("decode search provider metadata: %v", err)
		return
	}
	if metadata.Provider != websearch.ProviderSearXNG || metadata.Credits != 0 {
		t.Errorf("unexpected paid/non-SearXNG search response: provider=%q credits=%d", metadata.Provider, metadata.Credits)
	}
}

func TestNoTavilyLiveConfigIgnoresProviderEnvironment(t *testing.T) {
	t.Setenv("WEB_SEARCH_PROVIDER", "tavily")
	t.Setenv("WEB_SEARCH_TAVILY_FALLBACK", "true")
	t.Setenv("TAVILY_API_KEY", "offline-test-not-a-real-key")
	config := noTavilyLiveSearchConfig("http://127.0.0.1:8888")
	if config.Provider != websearch.ProviderSearXNG || config.TavilyFallback || config.TavilyAPIKey != "" {
		t.Fatalf("unsafe evaluation configuration: provider=%q fallback=%t key-present=%t",
			config.Provider, config.TavilyFallback, config.TavilyAPIKey != "")
	}
	if _, err := newNoTavilyLiveSearcher(config); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*websearch.Config){
		func(c *websearch.Config) { c.Provider = websearch.ProviderTavily },
		func(c *websearch.Config) { c.TavilyFallback = true },
		func(c *websearch.Config) { c.TavilyAPIKey = "offline-test-not-a-real-key" },
		func(c *websearch.Config) { c.SearXNGBaseURL = "https://API.TAVILY.COM.:443" },
		func(c *websearch.Config) { c.SearXNGBaseURL = "" },
	} {
		unsafe := config
		mutate(&unsafe)
		if _, err := newNoTavilyLiveSearcher(unsafe); err == nil {
			t.Error("unsafe evaluation configuration was accepted")
		}
	}
}

func TestLiveTavilyNetworkGuardBlocksHostsBeforeDelegating(t *testing.T) {
	var delegated, detected atomic.Int64
	guard := &liveTavilyBlockTransport{
		delegate: liveSafetyRoundTripper(func(r *http.Request) (*http.Response, error) {
			delegated.Add(1)
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
		}),
		onBlocked: func(string) { detected.Add(1) },
	}
	client := &http.Client{Transport: guard}
	for _, endpoint := range []string{"https://api.tavily.com/search", "https://TAVILY.COM", "https://other.api.TAVILY.COM.:443/search"} {
		_, err := client.Get(endpoint)
		if !errors.Is(err, errLiveTavilyBlocked) {
			t.Errorf("request to %s error=%v, want blocked", endpoint, err)
		}
	}
	if delegated.Load() != 0 || detected.Load() != 3 || guard.attempts.Load() != 3 {
		t.Fatalf("guard delegated=%d detected=%d attempted=%d", delegated.Load(), detected.Load(), guard.attempts.Load())
	}
	response, err := client.Get("https://not-tavily.example/search")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if delegated.Load() != 1 {
		t.Fatal("guard did not delegate a permitted destination")
	}
}

func TestNoTavilyLiveSearcherDoesNotFallbackOnEmptyOrError(t *testing.T) {
	guard := installLiveTavilyBlock(t)
	for _, scenario := range []struct {
		name   string
		status int
		body   string
	}{
		{"empty", http.StatusOK, `{"results":[],"unresponsive_engines":[]}`},
		{"unhealthy", http.StatusOK, `{"results":[],"unresponsive_engines":[["bing","timeout"]]}`},
		{"http_error", http.StatusBadGateway, `upstream unavailable`},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(scenario.status)
				fmt.Fprint(w, scenario.body)
			}))
			defer server.Close()
			searcher, err := newNoTavilyLiveSearcher(noTavilyLiveSearchConfig(server.URL))
			if err != nil {
				t.Fatal(err)
			}
			_, err = searcher.Execute(context.Background(), tool.Scope{}, json.RawMessage(
				`{"query":"test search","mode":"research","topic":"general","recency":"none","include_domains":[]}`))
			if err == nil {
				t.Error("empty/error SearXNG response unexpectedly succeeded")
			}
			_, _ = searcher.SearchNews(context.Background(), "test search")
			if requests.Load() < 2 || guard.attempts.Load() != 0 {
				t.Errorf("SearXNG requests=%d Tavily attempts=%d", requests.Load(), guard.attempts.Load())
			}
		})
	}
}

func TestLiveTavilyNetworkGuardBlocksRedirectsAndHostOverrides(t *testing.T) {
	var delegated, detected atomic.Int64
	guard := &liveTavilyBlockTransport{
		delegate: liveSafetyRoundTripper(func(r *http.Request) (*http.Response, error) {
			delegated.Add(1)
			return &http.Response{StatusCode: http.StatusFound,
				Header: http.Header{"Location": []string{"https://api.tavily.com/search"}},
				Body:   io.NopCloser(strings.NewReader(""))}, nil
		}),
		onBlocked: func(string) { detected.Add(1) },
	}
	client := &http.Client{Transport: guard}
	if _, err := client.Get("http://searxng.invalid/search"); !errors.Is(err, errLiveTavilyBlocked) {
		t.Errorf("Tavily redirect error=%v, want blocked", err)
	}
	request, err := http.NewRequest(http.MethodPost, "https://otherwise-safe.invalid/search", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "API.TAVILY.COM.:443"
	if _, err := client.Do(request); !errors.Is(err, errLiveTavilyBlocked) {
		t.Errorf("Tavily Host override error=%v, want blocked", err)
	}
	if delegated.Load() != 1 || detected.Load() != 2 || guard.attempts.Load() != 2 {
		t.Fatalf("guard delegated=%d detected=%d attempted=%d", delegated.Load(), detected.Load(), guard.attempts.Load())
	}
}

// Negative control: prove the actual production Tavily fallback adapter is
// intercepted, using a dummy key and an in-memory SearXNG error. No live API is
// contacted. The callback is collected instead of failing this expected case.
func TestLiveTavilyNetworkGuardCatchesActualFallback(t *testing.T) {
	previous := http.DefaultTransport
	var detected atomic.Int64
	guard := &liveTavilyBlockTransport{
		delegate: liveSafetyRoundTripper(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"results":[]}`)), Header: make(http.Header)}, nil
		}),
		onBlocked: func(string) { detected.Add(1) },
	}
	http.DefaultTransport = guard
	t.Cleanup(func() { http.DefaultTransport = previous })
	searcher, err := websearch.NewConfigured(websearch.Config{
		Provider: websearch.ProviderSearXNG, SearXNGBaseURL: "http://searxng.invalid",
		TavilyFallback: true, TavilyAPIKey: "offline-test-not-a-real-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = searcher.Execute(context.Background(), tool.Scope{}, json.RawMessage(
		`{"query":"test search","mode":"quick","topic":"general","recency":"none","include_domains":[]}`))
	if !errors.Is(err, errLiveTavilyBlocked) || guard.attempts.Load() != 1 || detected.Load() != 1 {
		t.Fatalf("fallback error=%v attempts=%d detected=%d; want blocked actual Tavily adapter", err, guard.attempts.Load(), detected.Load())
	}
}

type liveSafetyRoundTripper func(*http.Request) (*http.Response, error)

func (f liveSafetyRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
