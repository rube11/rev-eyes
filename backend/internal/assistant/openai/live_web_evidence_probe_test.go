package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

// TestLiveSearXNGEvidenceProbe inspects one real SearXNG research response, with
// static page extraction but no router/model/OpenAI request. It uses exactly the
// same no-key/no-fallback provider construction and provider HTTP guard as the
// live scenarios. Returned public webpages can still receive ordinary GETs.
func TestLiveSearXNGEvidenceProbe(t *testing.T) {
	if os.Getenv("RUN_LIVE_WEB_PROBE") != "1" {
		t.Skip("set RUN_LIVE_WEB_PROBE=1, SEARXNG_BASE_URL and LIVE_WEB_PROBE_QUERY to inspect SearXNG evidence without OpenAI; Tavily fallback is blocked")
	}
	arguments, err := liveEvidenceProbeArguments(os.Getenv("LIVE_WEB_PROBE_QUERY"), os.Getenv("LIVE_WEB_PROBE_DOMAINS"))
	if err != nil {
		t.Fatalf("invalid evidence probe input: %v", err)
	}
	guard := installLiveTavilyBlock(t)
	config := noTavilyLiveSearchConfig(requiredLiveEnv(t, "SEARXNG_BASE_URL"))
	searcher, err := newNoTavilyLiveSearcher(config)
	if err != nil {
		t.Fatalf("configure SearXNG-only probe: %v", err)
	}
	report := struct {
		StartedAt                  string                        `json:"started_at"`
		ElapsedMS                  int64                         `json:"elapsed_ms"`
		Arguments                  json.RawMessage               `json:"arguments"`
		Configuration              liveSearchSafetyConfiguration `json:"configuration"`
		OpenAICalls                int                           `json:"openai_calls"`
		Content                    string                        `json:"content"`
		Error                      string                        `json:"error,omitempty"`
		TavilyNetworkAttempts      int64                         `json:"tavily_network_attempts"`
		ZeroTavilyAttemptsVerified bool                          `json:"zero_tavily_attempts_verified"`
	}{
		StartedAt: time.Now().UTC().Format(time.RFC3339), Arguments: arguments,
		Configuration: liveSearchSafetyConfiguration{
			Provider: config.Provider, SearXNGBaseURL: config.SearXNGBaseURL,
			TavilyFallback: false, ResearchExtraction: config.ResearchExtraction,
			TavilyProviderNetworkBlocked: true,
			TavilyGuardScope:             "Tavily provider requests blocked by the default HTTP transport; initial and redirected page extraction URLs blocked by a shared pre-DNS policy. Not a machine-wide firewall.",
		},
	}
	var reportFile *os.File
	if path := os.Getenv("LIVE_WEB_PROBE_REPORT"); path != "" {
		reportFile, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatalf("create exclusive evidence report: %v", err)
		}
	}
	t.Cleanup(func() {
		report.TavilyNetworkAttempts = guard.attempts.Load()
		report.ZeroTavilyAttemptsVerified = report.TavilyNetworkAttempts == 0
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Errorf("encode evidence report: %v", err)
			return
		}
		t.Logf("SEARXNG_EVIDENCE_PROBE %s", encoded)
		if reportFile != nil {
			defer reportFile.Close()
			if _, err := reportFile.Write(append(encoded, '\n')); err != nil {
				t.Errorf("write evidence report: %v", err)
			}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	started := time.Now()
	result, err := searcher.Execute(ctx, tool.Scope{}, arguments)
	report.ElapsedMS = time.Since(started).Milliseconds()
	report.Content = result.Content
	if err != nil {
		report.Error = err.Error()
		t.Errorf("SearXNG evidence probe: %v", err)
	}
	assertNoTavilySearchResult(t, result.Content)
}

var liveProbeDomainPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)

func liveEvidenceProbeArguments(query, rawDomains string) (json.RawMessage, error) {
	query = strings.TrimSpace(query)
	if query == "" || utf8.RuneCountInString(query) > 400 {
		return nil, errors.New("LIVE_WEB_PROBE_QUERY must contain 1-400 characters")
	}
	domains := make([]string, 0)
	if strings.TrimSpace(rawDomains) != "" {
		parts := strings.Split(rawDomains, ",")
		if len(parts) > 5 {
			return nil, errors.New("LIVE_WEB_PROBE_DOMAINS permits at most five comma-separated bare hostnames")
		}
		seen := make(map[string]bool)
		for _, part := range parts {
			domain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(part)), "www.")
			if !liveProbeDomainPattern.MatchString(domain) || len(domain) > 253 {
				return nil, errors.New("LIVE_WEB_PROBE_DOMAINS requires nonempty bare hostnames, without schemes, paths, ports or credentials")
			}
			for _, label := range strings.Split(domain, ".") {
				if len(label) > 63 {
					return nil, errors.New("LIVE_WEB_PROBE_DOMAINS has an overlong hostname label")
				}
			}
			if !seen[domain] {
				domains = append(domains, domain)
				seen[domain] = true
			}
		}
	}
	return json.Marshal(liveSearchArguments{Query: query, Mode: "research", Topic: "general", Recency: "none", IncludeDomains: domains})
}

func TestLiveEvidenceProbeArgumentsAreBoundedAndProviderIndependent(t *testing.T) {
	t.Setenv("WEB_SEARCH_PROVIDER", "tavily")
	t.Setenv("WEB_SEARCH_TAVILY_FALLBACK", "true")
	t.Setenv("TAVILY_API_KEY", "offline-test-not-a-real-key")
	encoded, err := liveEvidenceProbeArguments("  Aurora telescope public viewing schedule  ", " WWW.Aurora.Example, observatory.example,aurora.example ")
	if err != nil {
		t.Fatal(err)
	}
	var arguments liveSearchArguments
	if err := json.Unmarshal(encoded, &arguments); err != nil {
		t.Fatal(err)
	}
	if arguments.Query != "Aurora telescope public viewing schedule" || arguments.Mode != "research" || arguments.Topic != "general" || arguments.Recency != "none" {
		t.Errorf("probe arguments=%#v", arguments)
	}
	if fmt.Sprint(arguments.IncludeDomains) != "[aurora.example observatory.example]" {
		t.Errorf("normalized domains=%v", arguments.IncludeDomains)
	}
	config := noTavilyLiveSearchConfig("http://127.0.0.1:8888")
	if config.Provider != "searxng" || config.TavilyFallback || config.TavilyAPIKey != "" || !config.ResearchExtraction {
		t.Fatal("probe construction policy must be SearXNG-only research with no Tavily key or fallback")
	}
}

func TestLiveEvidenceProbeArgumentsRejectInvalidInputsBeforeNetwork(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct{ name, query, domains string }{
		{"empty_query", " ", ""},
		{"long_query", strings.Repeat("星", 401), ""},
		{"domain_url", "Aurora telescope", "https://aurora.example"},
		{"domain_path", "Aurora telescope", "aurora.example/viewing"},
		{"domain_credentials", "Aurora telescope", "user@aurora.example"},
		{"domain_port", "Aurora telescope", "aurora.example:443"},
		{"empty_domain_between_commas", "Aurora telescope", "aurora.example,,observatory.example"},
		{"invalid_label", "Aurora telescope", "-aurora.example"},
		{"overlong_label", "Aurora telescope", strings.Repeat("a", 64) + ".example"},
		{"too_many_domains", "Aurora telescope", "a.example,b.example,c.example,d.example,e.example,f.example"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if _, err := liveEvidenceProbeArguments(scenario.query, scenario.domains); err == nil {
				t.Error("invalid probe input was accepted")
			}
		})
	}
	encoded, err := liveEvidenceProbeArguments(strings.Repeat("星", 400), "")
	if err != nil || !strings.Contains(string(encoded), `"include_domains":[]`) {
		t.Errorf("400-rune query with no domains must be accepted with []: error=%v", err)
	}
}
