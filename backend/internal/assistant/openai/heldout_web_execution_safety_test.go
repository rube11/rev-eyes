package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func TestHeldoutWebExecutionIssueChecksCompleteUnicodeResponseAndAttempts(t *testing.T) {
	for _, test := range []struct {
		name     string
		response string
		attempts int64
		valid    bool
	}{
		{name: "short_answer", response: "The requested detail could not be verified.", valid: true},
		{name: "exact_420_multibyte", response: strings.Repeat("星", 420), valid: true},
		{name: "421_multibyte", response: strings.Repeat("星", 421)},
		{name: "empty"},
		{name: "only_whitespace", response: " \t\r\n\u2003 "},
		{name: "citation_counts_toward_limit", response: strings.Repeat("a", 420) + " (aurora.example)"},
		{name: "caveat_counts_toward_limit", response: strings.Repeat("a", 419) + " Unverified."},
		{name: "trailing_space_is_part_of_full_response", response: strings.Repeat("a", 420) + " "},
		{name: "blocked_attempt_with_valid_answer", response: "A normal-looking answer.", attempts: 1},
		{name: "multiple_blocked_attempts", response: "A normal-looking answer.", attempts: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			issue := heldoutWebExecutionIssue(test.response, test.attempts)
			if (issue == "") != test.valid {
				t.Fatalf("execution issue=%q, want valid=%t; the complete response and Tavily attempts must both satisfy the contract", issue, test.valid)
			}
		})
	}
}

// A socket-free DialContext observes the standard transport's actual lookup
// target. These spellings resolve to Tavily destinations and must not evade a
// pre-transport guard simply because the URL contains non-ASCII characters.
func TestHeldoutTavilyGuardBlocksHTTPNormalizedHostnameSpellings(t *testing.T) {
	for _, test := range []struct{ host, canonicalHost string }{
		{"api.tavily.com\u3002", "api.tavily.com."},
		{"api\uff0etavily\uff0ecom", "api.tavily.com"},
		{"api\uff61tavily\uff61com", "api.tavily.com"},
		{"ａｐｉ．ｔａｖｉｌｙ．ｃｏｍ", "api.tavily.com"},
		{"api.ta\u00advily.com", "api.tavily.com"},
		{"api.ta\u200bvily.com", "api.tavily.com"},
	} {
		t.Run(test.host, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodGet, "https://"+test.host+"/public-page", nil)
			if err != nil {
				t.Fatal(err)
			}
			dialAddress := ""
			transport := &http.Transport{Proxy: nil, DialContext: func(_ context.Context, _, address string) (net.Conn, error) {
				dialAddress = address
				return nil, errors.New("offline fixture prevents DNS and socket creation")
			}}
			_, _ = transport.RoundTrip(request)
			transport.CloseIdleConnections()
			if dialAddress != net.JoinHostPort(test.canonicalHost, "443") {
				t.Fatalf("negative-control URL normalized to %q instead of expected HTTP target %q", dialAddress, test.canonicalHost)
			}
			delegated := 0
			guard := &liveTavilyBlockTransport{delegate: liveSafetyRoundTripper(func(*http.Request) (*http.Response, error) {
				delegated++
				return liveGuardResponse(http.StatusOK, "offline negative control"), nil
			})}
			response, err := guard.RoundTrip(request)
			if response != nil {
				response.Body.Close()
			}
			if !errors.Is(err, errLiveUnnormalizedHost) || delegated != 0 || guard.attempts.Load() != 0 || guard.unnormalizedAttempts.Load() != 1 {
				t.Errorf("HTTP-equivalent destination escaped conservative guard or was incorrectly classified: error=%v delegated=%d Tavily attempts=%d unnormalized=%d", err, delegated, guard.attempts.Load(), guard.unnormalizedAttempts.Load())
			}
			if _, err := newNoTavilyLiveSearcher(noTavilyLiveSearchConfig("https://" + test.host)); err == nil {
				t.Error("HTTP-equivalent Tavily hostname was accepted as the SearXNG endpoint")
			}
		})
	}
}

func TestHeldoutGuardDistinguishesUnnormalizedPublicHostsFromTavily(t *testing.T) {
	for _, test := range []struct {
		host    string
		allowed bool
	}{
		{host: "münich.example"},
		{host: "xn--mnich-kva.example", allowed: true},
		{host: "aurora.example", allowed: true},
	} {
		t.Run(test.host, func(t *testing.T) {
			delegated, detected := 0, 0
			guard := &liveTavilyBlockTransport{
				delegate: liveSafetyRoundTripper(func(*http.Request) (*http.Response, error) {
					delegated++
					return liveGuardResponse(http.StatusOK, "ordinary offline research"), nil
				}),
				onUnnormalized: func(host string) {
					detected++
					if host != test.host {
						t.Errorf("unnormalized diagnostic must contain only the hostname: %q", host)
					}
				},
			}
			request, err := http.NewRequest(http.MethodGet, "https://"+test.host+"/not-in-diagnostic?marker=not-in-diagnostic", nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := guard.RoundTrip(request)
			if response != nil {
				response.Body.Close()
			}
			if test.allowed {
				if err != nil || delegated != 1 || detected != 0 || guard.unnormalizedAttempts.Load() != 0 {
					t.Fatalf("ASCII/punycode public destination was unexpectedly blocked: error=%v", err)
				}
			} else if !errors.Is(err, errLiveUnnormalizedHost) || delegated != 0 || detected != 1 || guard.unnormalizedAttempts.Load() != 1 {
				t.Fatalf("raw Unicode destination did not fail closed: error=%v", err)
			}
			if guard.attempts.Load() != 0 || isTavilyHost(test.host) {
				t.Fatal("an unrelated internationalized/public hostname must not be reported as Tavily")
			}
		})
	}
}

func TestHeldoutGuardBlocksUnnormalizedExtractionBeforeDNS(t *testing.T) {
	previousTransport, previousResolver := http.DefaultTransport, net.DefaultResolver
	var dnsCalls, detected atomic.Int64
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		dnsCalls.Add(1)
		return nil, errors.New("offline fixture prevents DNS and socket creation")
	}}
	guard := &liveTavilyBlockTransport{
		delegate: liveSafetyRoundTripper(func(request *http.Request) (*http.Response, error) {
			if request.URL.Hostname() != "searxng.invalid" {
				t.Error("unexpected HTTP destination in offline fixture")
				return nil, errors.New("unexpected offline destination")
			}
			return liveGuardResponse(http.StatusOK, `{"results":[{"title":"Aurora reference","url":"https://api.tavily.com\u3002/reference","content":"Original discovery evidence.","score":1}]}`), nil
		}),
		onUnnormalized: func(string) { detected.Add(1) },
	}
	http.DefaultTransport = guard
	t.Cleanup(func() { http.DefaultTransport, net.DefaultResolver = previousTransport, previousResolver })
	searcher, err := newNoTavilyLiveSearcher(noTavilyLiveSearchConfig("https://searxng.invalid"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := searcher.Execute(context.Background(), tool.Scope{}, json.RawMessage(`{"query":"Aurora reference","mode":"research","topic":"general","recency":"none","include_domains":[]}`))
	if err != nil || !strings.Contains(result.Content, "Original discovery evidence.") {
		t.Fatalf("discovery must survive a blocked extraction: error=%v content=%q", err, result.Content)
	}
	if dnsCalls.Load() != 0 || detected.Load() != 1 || guard.unnormalizedAttempts.Load() != 1 || guard.attempts.Load() != 0 {
		t.Fatalf("raw-Unicode extraction was not stopped and separately counted before DNS: DNS=%d detected=%d unnormalized=%d Tavily=%d", dnsCalls.Load(), detected.Load(), guard.unnormalizedAttempts.Load(), guard.attempts.Load())
	}
}
