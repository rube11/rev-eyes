package openai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const capturedEvidenceLimit = 16 << 20

type capturedEvidenceReport struct {
	StartedAt                  string                        `json:"started_at"`
	Configuration              liveSearchSafetyConfiguration `json:"configuration"`
	TavilyNetworkAttempts      int64                         `json:"tavily_network_attempts"`
	ZeroTavilyAttemptsVerified bool                          `json:"zero_tavily_attempts_verified"`
	Runs                       []liveComparisonRun           `json:"runs"`
}

type capturedEvidenceFixture struct {
	Content          string
	CapturedSearches []liveComparisonSearch
	ResultCount      int
}

func loadCapturedEvidence(reader io.Reader) (map[string]capturedEvidenceFixture, time.Time, string, error) {
	data, err := io.ReadAll(io.LimitReader(reader, capturedEvidenceLimit+1))
	if err != nil {
		return nil, time.Time{}, "", err
	}
	if len(data) > capturedEvidenceLimit {
		return nil, time.Time{}, "", errors.New("captured evidence report exceeds the 16 MiB limit")
	}
	var report capturedEvidenceReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, time.Time{}, "", errors.New("captured evidence report is not valid report JSON")
	}
	asOf, err := time.Parse(time.RFC3339, report.StartedAt)
	if err != nil {
		return nil, time.Time{}, "", errors.New("captured evidence requires an offset-qualified original started_at")
	}
	if report.Configuration.Provider != "searxng" || report.Configuration.TavilyFallback || report.TavilyNetworkAttempts != 0 || !report.ZeroTavilyAttemptsVerified {
		return nil, time.Time{}, "", errors.New("captured report must document SearXNG-only retrieval with zero Tavily attempts")
	}
	fixtures := make(map[string]capturedEvidenceFixture)
	known := make(map[string]liveEyesWebScenario)
	for _, scenario := range liveEyesWebScenarios() {
		known[scenario.name] = scenario
	}
	for _, run := range report.Runs {
		scenario, ok := known[run.Scenario]
		if !ok || run.Spoken != scenario.spoken || run.Arm != "searxng_only" {
			return nil, time.Time{}, "", fmt.Errorf("captured scenario %q does not match the current synthetic SearXNG fixture", run.Scenario)
		}
		if _, exists := fixtures[run.Scenario]; exists {
			return nil, time.Time{}, "", fmt.Errorf("duplicate captured scenario %q", run.Scenario)
		}
		var rows []json.RawMessage
		fixture := capturedEvidenceFixture{CapturedSearches: append([]liveComparisonSearch(nil), run.Searches...)}
		for _, search := range run.Searches {
			if search.Error != "" {
				continue
			}
			var payload struct {
				Provider string            `json:"provider"`
				Credits  int               `json:"credits"`
				Results  []json.RawMessage `json:"results"`
			}
			if json.Unmarshal([]byte(search.Content), &payload) != nil || payload.Provider != "searxng" || payload.Credits != 0 {
				return nil, time.Time{}, "", fmt.Errorf("captured scenario %q includes invalid or non-SearXNG evidence", run.Scenario)
			}
			rows = append(rows, payload.Results...)
		}
		if rows == nil {
			rows = []json.RawMessage{}
		}
		// Only original result rows enter the tool fixture. Previous answers,
		// grading verdicts and evidence-review candidates are never model input.
		content, err := json.Marshal(map[string]any{
			"provider": "searxng", "results": rows, "credits": 0,
			"retrieval_mode": "captured_fixed_corpus", "query_ignored": true,
			"live_search_performed": false, "original_report_started_at": report.StartedAt,
			"notice": "This evaluation returns the same captured scenario corpus for every query. No new search or extraction was performed; requested filters and missing-source lookups cannot expand this evidence.",
		})
		if err != nil {
			return nil, time.Time{}, "", err
		}
		fixture.Content, fixture.ResultCount = string(content), len(rows)
		fixtures[run.Scenario] = fixture
	}
	if len(fixtures) == 0 {
		return nil, time.Time{}, "", errors.New("captured evidence report has no scenarios")
	}
	digest := sha256.Sum256(data)
	return fixtures, asOf, hex.EncodeToString(digest[:]), nil
}

// A fixed corpus has no network client or live delegate. Arguments are recorded
// by comparisonSearchTool but never used to request more evidence.
type capturedEvidenceTool struct {
	spec    tool.Spec
	content string
}

func (r *capturedEvidenceTool) Spec() tool.Spec { return r.spec }
func (r *capturedEvidenceTool) Execute(ctx context.Context, _ tool.Scope, _ json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	return tool.Result{Content: r.content}, nil
}

type liveEvidenceReplayRun struct {
	Scenario                   string                    `json:"scenario"`
	Spoken                     string                    `json:"spoken"`
	Response                   string                    `json:"response"`
	Error                      string                    `json:"error,omitempty"`
	ElapsedMS                  int64                     `json:"elapsed_ms"`
	CapturedResultCount        int                       `json:"captured_result_count"`
	EvaluationChecksPassed     bool                      `json:"evaluation_checks_passed"`
	CapturedEvidenceQuality    liveQualityAssessment     `json:"captured_evidence_quality"`
	CapturedSearches           []liveComparisonSearch    `json:"captured_searches"`
	ReplaySearchCalls          []liveComparisonSearch    `json:"replay_search_calls"`
	EvidenceReviews            []webEvidenceReviewRecord `json:"evidence_reviews"`
	TavilyNetworkAttempts      int64                     `json:"tavily_network_attempts"`
	ZeroTavilyAttemptsVerified bool                      `json:"zero_tavily_attempts_verified"`
}

// TestLiveEyesCapturedEvidenceReplay spends OpenAI credits, but performs NO
// live search, extraction, routing or memory retrieval. It is a synthesis-only
// fixed-corpus experiment, not a successful live retrieval/search-plan result.
func TestLiveEyesCapturedEvidenceReplay(t *testing.T) {
	if os.Getenv("RUN_LIVE_WEB_EVIDENCE_REPLAY") != "1" {
		t.Skip("set RUN_LIVE_WEB_EVIDENCE_REPLAY=1 to spend OpenAI credits on captured evidence; no live search")
	}
	guard := installLiveTavilyBlock(t)
	sourcePath, err := filepath.Abs(requiredLiveEnv(t, "LIVE_WEB_REPLAY_SOURCE_REPORT"))
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatalf("open captured report: %v", err)
	}
	fixtures, asOf, sourceDigest, loadErr := loadCapturedEvidence(source)
	if closeErr := source.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	model := requiredLiveEnv(t, "OPENAI_AGENT_MODEL")
	// Reserve a NEW report before any paid requests. No input/output overwrite.
	output, err := os.OpenFile(requiredLiveEnv(t, "LIVE_WEB_REPLAY_REPORT"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatalf("create exclusive replay report: %v", err)
	}
	report := struct {
		StartedAt                  string                  `json:"started_at"`
		EvidenceAsOf               string                  `json:"evidence_as_of"`
		SourceReport               string                  `json:"source_report"`
		SourceReportSHA256         string                  `json:"source_report_sha256"`
		AgentModel                 string                  `json:"agent_model"`
		Mode                       string                  `json:"mode"`
		QueryIgnored               bool                    `json:"query_ignored"`
		LiveSearchPerformed        bool                    `json:"live_search_performed"`
		LiveOpenAICallsEnabled     bool                    `json:"live_openai_calls_enabled"`
		Scope                      string                  `json:"scope"`
		Policy                     string                  `json:"source_bound_review_policy"`
		TavilyNetworkAttempts      int64                   `json:"tavily_network_attempts"`
		ZeroTavilyAttemptsVerified bool                    `json:"zero_tavily_attempts_verified"`
		Runs                       []liveEvidenceReplayRun `json:"runs"`
	}{StartedAt: time.Now().UTC().Format(time.RFC3339), EvidenceAsOf: asOf.Format(time.RFC3339), SourceReport: sourcePath, SourceReportSHA256: sourceDigest,
		AgentModel: model, Mode: "captured_fixed_corpus_synthesis", QueryIgnored: true, LiveOpenAICallsEnabled: true,
		Scope:  "Synthetic scenario inputs and only raw captured result rows are supplied to the live agent. Every query/filter receives the same scenario corpus; no SearXNG, extraction, Tavily, router or memory-service call is made. Original report started_at freezes agent and quality time in America/Los_Angeles. Quality describes captured evidence, not current retrieval; routing/search-plan/retrieval-success assertions are intentionally absent. Elapsed time includes live OpenAI synthesis, not live retrieval latency. Manual claim entailment review remains required.",
		Policy: "Exact gpt-5.4-mini and gpt-5.4-mini-2026-03-17: ordinary post-search low/2048, source-bound review low/3072 after SearXNG evidence, at most one repair within the existing round budget, final round tools disabled. Mechanical source/quote/number/time checks do not prove semantic entailment."}
	t.Cleanup(func() {
		defer output.Close()
		report.TavilyNetworkAttempts = guard.attempts.Load()
		report.ZeroTavilyAttemptsVerified = report.TavilyNetworkAttempts == 0
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			t.Errorf("write replay report: %v", err)
		}
	})
	key := requiredLiveEnv(t, "OPENAI_API_KEY")
	// Retrieve only the production tool schema; Execute is never delegated.
	schemaOnly, err := newNoTavilyLiveSearcher(noTavilyLiveSearchConfig("https://captured-replay.invalid"))
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range liveEyesWebScenarios() {
		fixture, ok := fixtures[scenario.name]
		if !ok {
			continue // A deliberately selected subset remains identified in the report.
		}
		t.Run(scenario.name, func(t *testing.T) {
			attemptsBefore := guard.attempts.Load()
			recorded := &comparisonSearchTool{delegate: &capturedEvidenceTool{spec: schemaOnly.Spec(), content: fixture.Content}}
			registry := tool.NewRegistry()
			if err := registry.Register(recorded); err != nil {
				t.Fatal(err)
			}
			executor, err := tool.NewExecutor(registry)
			if err != nil {
				t.Fatal(err)
			}
			agent, err := NewAgent(key, model, registry, executor)
			if err != nil {
				t.Fatal(err)
			}
			agent.now = func() time.Time { return asOf }
			run := liveEvidenceReplayRun{Scenario: scenario.name, Spoken: scenario.spoken, CapturedResultCount: fixture.ResultCount, CapturedSearches: fixture.CapturedSearches, EvidenceReviews: []webEvidenceReviewRecord{}}
			agent.onWebEvidenceReview = func(review webEvidenceReviewRecord) { run.EvidenceReviews = append(run.EvidenceReviews, review) }
			defer func() {
				run.TavilyNetworkAttempts = guard.attempts.Load() - attemptsBefore
				run.ZeroTavilyAttemptsVerified = run.TavilyNetworkAttempts == 0
				run.EvaluationChecksPassed = !t.Failed()
				report.Runs = append(report.Runs, run)
				encoded, _ := json.Marshal(run)
				t.Logf("CAPTURED_EVIDENCE_REPLAY %s", encoded)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			started := time.Now()
			response, runErr := agent.Respond(ctx, tool.Scope{TimeZone: "America/Los_Angeles"}, scenario.spoken, session.Conversation{}, scenario.memories)
			run.ElapsedMS, run.Response, run.ReplaySearchCalls = time.Since(started).Milliseconds(), response, recorded.snapshot()
			run.CapturedEvidenceQuality = evaluateLiveSearchQuality(scenario, response, fixture.CapturedSearches, asOf)
			run.CapturedEvidenceQuality.ManualReviewRequired = append(run.CapturedEvidenceQuality.ManualReviewRequired, "Fixed-corpus replay ignores new queries and filters. Passing these checks establishes neither live retrieval success nor factual entailment; check every rendered and rejected claim against the original rows.")
			if runErr != nil {
				run.Error = runErr.Error()
				t.Errorf("replay synthesis: %v", runErr)
			}
			if len(run.ReplaySearchCalls) == 0 {
				t.Error("agent did not consume the captured evidence tool")
			}
			for _, check := range run.CapturedEvidenceQuality.Checks {
				if !check.Passed {
					t.Errorf("captured-evidence quality check failed: %+v", check)
				}
			}
		})
	}
}

func capturedOfflineReport(t *testing.T) capturedEvidenceReport {
	t.Helper()
	scenario := liveEyesWebScenarios()[0]
	return capturedEvidenceReport{StartedAt: "2026-09-07T06:59:00Z", Configuration: liveSearchSafetyConfiguration{Provider: "searxng"}, ZeroTavilyAttemptsVerified: true,
		Runs: []liveComparisonRun{{Scenario: scenario.name, Spoken: scenario.spoken, Arm: "searxng_only", Response: "PRIOR_ANSWER_SENTINEL", EvidenceReviews: []webEvidenceReviewRecord{{CandidateJSON: "PRIOR_REVIEW_SENTINEL"}},
			Searches: []liveComparisonSearch{{Arguments: json.RawMessage(`{"query":"Aurora"}`), Content: `{"provider":"searxng","results":[{"url":"https://aurora.example/info","snippet":"Aurora telescope evidence.","published_date":"2026-09-06"}]}`}}}}}
}

func TestCapturedEvidenceReplayLoaderExcludesPriorAnswersAndPreservesClock(t *testing.T) {
	t.Parallel()
	report := capturedOfflineReport(t)
	encoded, _ := json.Marshal(report)
	fixtures, asOf, digest, err := loadCapturedEvidence(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	fixture := fixtures[report.Runs[0].Scenario]
	if strings.Contains(fixture.Content, "SENTINEL") || !strings.Contains(fixture.Content, `"query_ignored":true`) || !strings.Contains(fixture.Content, `"published_date":"2026-09-06"`) || fixture.ResultCount != 1 || len(digest) != 64 {
		t.Errorf("invalid replay fixture or prior-answer leakage: %s", fixture.Content)
	}
	location, _ := time.LoadLocation("America/Los_Angeles")
	local := asOf.In(location)
	if local.Format("2006-01-02 15:04 Monday") != "2026-09-06 23:59 Sunday" || weekStart(local).Format("2006-01-02") != "2026-08-31" {
		t.Errorf("original local date/week changed at UTC midnight boundary: %s", local)
	}
}

func TestCapturedEvidenceReplayRetainsEverySourceVariantButNotFailedOutputs(t *testing.T) {
	t.Parallel()
	report := capturedOfflineReport(t)
	report.Runs[0].Searches = append(report.Runs[0].Searches,
		liveComparisonSearch{Arguments: json.RawMessage(`{"query":"Aurora opening hours"}`), Content: `{"provider":"searxng","results":[{"url":"https://aurora.example/info","snippet":"Aurora opens 8am to 5pm.","published_date":"2026-09-06T18:00:00Z"}]}`},
		liveComparisonSearch{Arguments: json.RawMessage(`{"query":"Failed later lookup"}`), Error: "captured timeout", Content: `FAILED_RESULT_SENTINEL`})
	report.Runs[0].Quality.ManualReviewRequired = []string{"PRIOR_VERDICT_SENTINEL"}
	encoded, _ := json.Marshal(report)
	fixtures, _, _, err := loadCapturedEvidence(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	fixture := fixtures[report.Runs[0].Scenario]
	var payload struct {
		Results []webEvidenceSource `json:"results"`
	}
	if err := json.Unmarshal([]byte(fixture.Content), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Results) != 2 || payload.Results[0].URL != payload.Results[1].URL || payload.Results[0].Snippet == payload.Results[1].Snippet || fixture.ResultCount != 2 {
		t.Errorf("same-URL evidence variants were replaced: %#v", payload.Results)
	}
	if strings.Contains(fixture.Content, "SENTINEL") || len(fixture.CapturedSearches) != 3 || fixture.CapturedSearches[2].Error == "" {
		t.Error("model fixture leaked failed output/prior verdict or audit trail lost captured error")
	}
}

func TestCapturedEvidenceReplayLoaderRejectsUnsafeOrAmbiguousReports(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name string
		edit func(*capturedEvidenceReport)
	}{
		{"missing_time", func(r *capturedEvidenceReport) { r.StartedAt = "" }},
		{"missing_offset", func(r *capturedEvidenceReport) { r.StartedAt = "2026-09-03T22:00:00" }},
		{"tavily_config", func(r *capturedEvidenceReport) { r.Configuration.Provider = "tavily" }},
		{"fallback", func(r *capturedEvidenceReport) { r.Configuration.TavilyFallback = true }},
		{"guard_attempt", func(r *capturedEvidenceReport) { r.TavilyNetworkAttempts = 1 }},
		{"no_guard_proof", func(r *capturedEvidenceReport) { r.ZeroTavilyAttemptsVerified = false }},
		{"wrong_fixture", func(r *capturedEvidenceReport) { r.Runs[0].Spoken = "Different prompt" }},
		{"duplicate_scenario", func(r *capturedEvidenceReport) { r.Runs = append(r.Runs, r.Runs[0]) }},
		{"tavily_row", func(r *capturedEvidenceReport) { r.Runs[0].Searches[0].Content = `{"provider":"tavily","results":[]}` }},
		{"paid_row", func(r *capturedEvidenceReport) {
			r.Runs[0].Searches[0].Content = `{"provider":"searxng","credits":1,"results":[]}`
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			report := capturedOfflineReport(t)
			scenario.edit(&report)
			encoded, _ := json.Marshal(report)
			if _, _, _, err := loadCapturedEvidence(strings.NewReader(string(encoded))); err == nil {
				t.Error("unsafe/ambiguous captured report accepted")
			}
		})
	}
}

func TestCapturedEvidenceToolIgnoresAllQueriesWithoutNetwork(t *testing.T) {
	guard := installLiveTavilyBlock(t)
	guard.delegate = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("fixed captured corpus unexpectedly attempted network")
		return nil, errors.New("network forbidden")
	})
	report := capturedOfflineReport(t)
	encoded, _ := json.Marshal(report)
	fixtures, _, _, err := loadCapturedEvidence(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	fixture := fixtures[report.Runs[0].Scenario]
	fixed := &capturedEvidenceTool{content: fixture.Content}
	for _, args := range []string{`{"query":"Aurora telescope"}`, `{"query":"Unknown future source","include_domains":["api.tavily.com"]}`} {
		result, err := fixed.Execute(context.Background(), tool.Scope{}, json.RawMessage(args))
		if err != nil || result.Content != fixture.Content || !strings.Contains(result.Content, `"live_search_performed":false`) {
			t.Errorf("fixed corpus execution changed with query: error=%v content=%s", err, result.Content)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixed.Execute(ctx, tool.Scope{}, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled replay executed: %v", err)
	}
}
