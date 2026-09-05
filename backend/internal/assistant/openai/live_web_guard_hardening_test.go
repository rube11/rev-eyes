package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/tool"
	"github.com/rube11/rev-eyes/backend/internal/tool/websearch"
)

// This negative control makes the guard's boundary explicit without a DNS lookup
// or socket. A separate extraction client must enforce the URL policy itself;
// replacing DefaultTransport alone cannot establish zero Tavily-host GETs.
func TestLiveGuardNegativeControlExplicitTransportIsIndependent(t *testing.T) {
	previous := http.DefaultTransport
	var explicitRequests atomic.Int64
	guard := &liveTavilyBlockTransport{delegate: liveSafetyRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Error("default transport should not be consulted by the explicit client")
		return nil, errors.New("unexpected default transport request")
	})}
	http.DefaultTransport = guard
	t.Cleanup(func() { http.DefaultTransport = previous })
	client := &http.Client{Transport: liveSafetyRoundTripper(func(request *http.Request) (*http.Response, error) {
		explicitRequests.Add(1)
		if request.URL.Hostname() != "tavily.com" || request.Method != http.MethodGet {
			t.Error("unexpected negative-control request")
		}
		return liveGuardResponse(http.StatusOK, "offline public page"), nil
	})}
	response, err := client.Get("https://tavily.com/public-research")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if explicitRequests.Load() != 1 || guard.attempts.Load() != 0 {
		t.Fatalf("explicit requests=%d default-guard attempts=%d", explicitRequests.Load(), guard.attempts.Load())
	}
}

func TestLiveGuardHostBoundaryAndDiagnosticRedaction(t *testing.T) {
	var delegated atomic.Int64
	var notices []string
	guard := &liveTavilyBlockTransport{
		delegate: liveSafetyRoundTripper(func(*http.Request) (*http.Response, error) {
			delegated.Add(1)
			return liveGuardResponse(http.StatusOK, "ordinary public research"), nil
		}),
		onBlocked: func(host string) { notices = append(notices, host) },
	}
	client := &http.Client{Transport: guard}
	for _, rawURL := range []string{
		"https://API.TAVILY.COM.:443/private-path?marker=not-for-diagnostics",
		"https://nested.api.tavily.com/",
	} {
		request, err := http.NewRequest(http.MethodPost, rawURL, strings.NewReader("body-not-for-diagnostics"))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-Test-Marker", "header-not-for-diagnostics")
		if _, err := client.Do(request); !errors.Is(err, errLiveTavilyBlocked) {
			t.Errorf("expected Tavily host to be rejected, got %v", err)
		}
	}
	if delegated.Load() != 0 || guard.attempts.Load() != 2 || !reflect.DeepEqual(notices, []string{"api.tavily.com", "nested.api.tavily.com"}) {
		t.Fatalf("blocked request was delegated or diagnostic contains more than normalized hostnames: delegated=%d attempts=%d notices=%q", delegated.Load(), guard.attempts.Load(), notices)
	}
	for _, host := range []string{"tavily.com.example", "not-tavily.com", "tavily.example", "aurora.example"} {
		response, err := client.Get("https://" + host + "/public-page")
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
	if delegated.Load() != 4 || guard.attempts.Load() != 2 {
		t.Fatal("guard must permit unrelated public hosts without changing the Tavily attempt count")
	}
}

func TestLiveGuardNoFallbackOnEntirelyInMemoryProviderFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		err    error
	}{
		{name: "empty", status: http.StatusOK, body: `{"results":[]}`},
		{name: "malformed", status: http.StatusOK, body: `{"results":`},
		{name: "bad_gateway", status: http.StatusBadGateway, body: "unavailable"},
		{name: "transport_failure", err: errors.New("offline simulated transport failure")},
		{name: "deadline", err: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := http.DefaultTransport
			var delegated atomic.Int64
			guard := &liveTavilyBlockTransport{delegate: liveSafetyRoundTripper(func(request *http.Request) (*http.Response, error) {
				delegated.Add(1)
				if request.URL.Hostname() != "searxng.invalid" {
					t.Error("unexpected destination; this fixture cannot access the network")
					return nil, errors.New("unexpected offline destination")
				}
				if test.err != nil {
					return nil, test.err
				}
				return liveGuardResponse(test.status, test.body), nil
			})}
			http.DefaultTransport = guard
			t.Cleanup(func() { http.DefaultTransport = previous })
			searcher, err := newNoTavilyLiveSearcher(noTavilyLiveSearchConfig("https://searxng.invalid"))
			if err != nil {
				t.Fatal(err)
			}
			result, err := searcher.Execute(context.Background(), tool.Scope{}, json.RawMessage(`{"query":"Aurora telescope schedule","mode":"research","topic":"general","recency":"none","include_domains":[]}`))
			if err == nil || result.Content != "" {
				t.Errorf("failed discovery unexpectedly supplied a result: error=%v content=%q", err, result.Content)
			}
			_, _ = searcher.SearchNews(context.Background(), "Aurora telescope announcement")
			if delegated.Load() != 2 || guard.attempts.Load() != 0 {
				t.Fatalf("provider requests=%d Tavily attempts=%d; want two bounded failed requests without fallback", delegated.Load(), guard.attempts.Load())
			}
		})
	}
}

func TestLiveGuardResearchExtractionRejectsTavilyBeforeDNS(t *testing.T) {
	for _, guarded := range []bool{true, false} {
		name := "without_default_guard"
		if guarded {
			name = "with_shared_default_guard"
		}
		t.Run(name, func(t *testing.T) {
			var discoveryCalls, dnsCalls, detections, suppliedPolicyCalls atomic.Int64
			previousTransport, previousResolver := http.DefaultTransport, net.DefaultResolver
			net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
				dnsCalls.Add(1)
				return nil, errors.New("offline fixture forbids DNS and sockets")
			}}
			delegate := liveSafetyRoundTripper(func(request *http.Request) (*http.Response, error) {
				discoveryCalls.Add(1)
				if request.URL.Hostname() != "searxng.invalid" {
					t.Error("unexpected HTTP destination in offline fixture")
					return nil, errors.New("unexpected offline destination")
				}
				return liveGuardResponse(http.StatusOK, `{"results":[{"title":"Aurora telescope report","url":"https://API.TAVILY.COM.:443/aurora","content":"Aurora telescope discovery snippet.","score":1}]}`), nil
			})
			guard := &liveTavilyBlockTransport{delegate: delegate, onBlocked: func(host string) {
				detections.Add(1)
				if host != "api.tavily.com" {
					t.Error("extraction guard must report only the normalized hostname")
				}
			}}
			http.DefaultTransport = delegate
			if guarded {
				http.DefaultTransport = guard
			}
			t.Cleanup(func() { http.DefaultTransport, net.DefaultResolver = previousTransport, previousResolver })
			config := noTavilyLiveSearchConfig("https://searxng.invalid")
			config.ExtractionURLPolicy = func(*url.URL) error {
				suppliedPolicyCalls.Add(1)
				return nil // A caller cannot weaken the live runner's extraction block.
			}
			searcher, err := newNoTavilyLiveSearcher(config)
			if err != nil {
				t.Fatal(err)
			}
			result, err := searcher.Execute(context.Background(), tool.Scope{}, json.RawMessage(`{"query":"Aurora telescope report","mode":"research","topic":"general","recency":"none","include_domains":[]}`))
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				Provider  string             `json:"provider"`
				Extracted int                `json:"extracted_results"`
				Results   []websearch.Result `json:"results"`
			}
			if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Provider != "searxng" || payload.Extracted != 0 || len(payload.Results) != 1 || payload.Results[0].Snippet != "Aurora telescope discovery snippet." {
				t.Fatalf("blocked extraction must retain only original discovery evidence: %#v", payload)
			}
			wantAttempts := int64(0)
			if guarded {
				wantAttempts = 1
			}
			if discoveryCalls.Load() != 1 || dnsCalls.Load() != 0 || suppliedPolicyCalls.Load() != 0 || detections.Load() != wantAttempts || guard.attempts.Load() != wantAttempts {
				t.Fatalf("discovery=%d DNS=%d supplied-policy=%d detections=%d attempts=%d; want one discovery and pre-DNS rejection", discoveryCalls.Load(), dnsCalls.Load(), suppliedPolicyCalls.Load(), detections.Load(), guard.attempts.Load())
			}
		})
	}
}

func TestLiveGuardResearchStillPermitsOrdinaryPublicHostValidation(t *testing.T) {
	previousTransport, previousResolver := http.DefaultTransport, net.DefaultResolver
	var dnsCalls atomic.Int64
	// Deliberately stop at DNS, before any socket. Reaching this resolver shows
	// ordinary research was permitted by the evaluation-specific URL policy.
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		dnsCalls.Add(1)
		return nil, errors.New("offline fixture stops ordinary public-page resolution")
	}}
	guard := &liveTavilyBlockTransport{delegate: liveSafetyRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Hostname() != "searxng.invalid" {
			t.Error("unexpected HTTP destination in offline fixture")
			return nil, errors.New("unexpected offline destination")
		}
		return liveGuardResponse(http.StatusOK, `{"results":[{"title":"Aurora Observatory","url":"https://aurora.example/schedule","content":"Aurora public viewing schedule.","score":1}]}`), nil
	})}
	http.DefaultTransport = guard
	t.Cleanup(func() { http.DefaultTransport, net.DefaultResolver = previousTransport, previousResolver })
	searcher, err := newNoTavilyLiveSearcher(noTavilyLiveSearchConfig("https://searxng.invalid"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := searcher.Execute(context.Background(), tool.Scope{}, json.RawMessage(`{"query":"Aurora telescope schedule","mode":"research","topic":"general","recency":"none","include_domains":[]}`))
	if err != nil || !strings.Contains(result.Content, "Aurora public viewing schedule.") {
		t.Fatalf("ordinary discovery must survive a simulated extraction failure: error=%v content=%q", err, result.Content)
	}
	if dnsCalls.Load() == 0 || guard.attempts.Load() != 0 {
		t.Fatalf("ordinary public host did not reach DNS validation or was counted as Tavily: DNS=%d attempts=%d", dnsCalls.Load(), guard.attempts.Load())
	}
}

func TestLiveGuardConfiguredPolicyRunsBeforeDNSAndPreservesSSRFChecks(t *testing.T) {
	for _, reject := range []bool{true, false} {
		name := "policy_allows_private_target_but_SSRF_rejects"
		if reject {
			name = "policy_rejects_before_DNS"
		}
		t.Run(name, func(t *testing.T) {
			previousTransport, previousResolver := http.DefaultTransport, net.DefaultResolver
			var policyCalls, dnsCalls, httpCalls atomic.Int64
			target := "http://127.0.0.1/schedule"
			if reject {
				target = "https://aurora.example/schedule"
			}
			net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
				dnsCalls.Add(1)
				return nil, errors.New("offline fixture forbids DNS and sockets")
			}}
			http.DefaultTransport = liveSafetyRoundTripper(func(request *http.Request) (*http.Response, error) {
				httpCalls.Add(1)
				if request.URL.Hostname() != "searxng.invalid" {
					t.Error("unexpected HTTP destination in offline fixture")
					return nil, errors.New("unexpected offline destination")
				}
				return liveGuardResponse(http.StatusOK, `{"results":[{"title":"Aurora Observatory","url":"`+target+`","content":"Original discovery.","score":1}]}`), nil
			})
			t.Cleanup(func() { http.DefaultTransport, net.DefaultResolver = previousTransport, previousResolver })
			config := noTavilyLiveSearchConfig("https://searxng.invalid")
			config.ExtractionURLPolicy = func(candidate *url.URL) error {
				policyCalls.Add(1)
				if candidate.String() != target {
					t.Error("policy did not receive the exact source URL")
				}
				if reject {
					return errors.New("offline additional extraction policy denial")
				}
				return nil
			}
			// Exercise the production factory directly, not the live constructor
			// that intentionally replaces caller-supplied extraction policy.
			searcher, err := websearch.NewConfigured(config)
			if err != nil {
				t.Fatal(err)
			}
			result, err := searcher.Execute(context.Background(), tool.Scope{}, json.RawMessage(`{"query":"Aurora telescope schedule","mode":"research","topic":"general","recency":"none","include_domains":[]}`))
			if err != nil || !strings.Contains(result.Content, "Original discovery.") {
				t.Fatalf("discovery must survive policy/SSRF rejection: error=%v content=%q", err, result.Content)
			}
			if policyCalls.Load() != 1 || dnsCalls.Load() != 0 || httpCalls.Load() != 1 {
				t.Fatalf("policy=%d DNS=%d HTTP=%d; expected extraction stopped before DNS/socket", policyCalls.Load(), dnsCalls.Load(), httpCalls.Load())
			}
		})
	}
}

func TestLiveComparisonRecorderPreservesParentLinkedRowsAndFailedContent(t *testing.T) {
	const content = `{"provider":"searxng","credits":0,"extracted_results":2,"results":[{"title":"Aurora Observatory","url":"https://aurora.example/","snippet":"Discovery and parent evidence.","score":8.5,"published_date":"2026-09-03"},{"title":"Aurora Viewing Schedule","url":"https://aurora.example/schedule","snippet":"Child schedule evidence.","score":0,"published_date":"2026-09-04","discovered_from":"https://aurora.example/"}]}`
	for _, test := range []struct {
		name    string
		content string
		err     error
	}{
		{name: "success_with_linked_source", content: content},
		{name: "partial_content_and_error", content: content, err: errors.New("offline partial extraction failure")},
		{name: "empty_failed_search", err: errors.New("offline search unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			delegate := &recordingTool{name: "search_web", result: tool.Result{Content: test.content}, err: test.err}
			recorder := &comparisonSearchTool{delegate: delegate}
			arguments := json.RawMessage(`{"query":"Aurora telescope schedule"}`)
			originalArguments := string(arguments)
			result, err := recorder.Execute(context.Background(), tool.Scope{}, arguments)
			if result.Content != test.content || !errors.Is(err, test.err) {
				t.Fatal("recorder changed the delegate's output or error")
			}
			arguments[0] = '[' // Caller mutations must not change the recorded request.
			searches := recorder.snapshot()
			if len(searches) != 1 || searches[0].Content != test.content || string(searches[0].Arguments) != originalArguments {
				t.Fatalf("recording omitted or changed a search output: %#v", searches)
			}
			if test.err != nil && searches[0].Error != test.err.Error() {
				t.Fatalf("recording omitted search failure: %#v", searches[0])
			}
			encoded, err := json.Marshal(liveComparisonRun{Searches: searches})
			if err != nil {
				t.Fatal(err)
			}
			var decoded liveComparisonRun
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded.Searches, searches) {
				t.Fatal("JSON report serialization changed the search's raw content or failure")
			}
			if test.content != "" {
				var payload struct {
					Results []websearch.Result `json:"results"`
				}
				if err := json.Unmarshal([]byte(decoded.Searches[0].Content), &payload); err != nil {
					t.Fatal(err)
				}
				if len(payload.Results) != 2 || payload.Results[1].DiscoveredFrom != payload.Results[0].URL || payload.Results[1].URL == payload.Results[0].URL || payload.Results[1].PublishedDate != "2026-09-04" || payload.Results[1].Score != 0 {
					t.Fatalf("linked source provenance was lost: %#v", payload.Results)
				}
			}
		})
	}
}

func liveGuardResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
