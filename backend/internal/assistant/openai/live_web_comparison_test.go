package openai

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/assistant"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

// TestLiveEyesWebSearchComparison retains the old runner name and opt-in as an
// alias, but now evaluates SearXNG ONLY. Tavily is disabled in construction and
// its provider client is blocked at the default HTTP transport. This still
// sends synthetic fixture context to OpenAI and the configured search instance
// and spends OpenAI credits.
func TestLiveEyesWebSearchComparison(t *testing.T) {
	if os.Getenv("RUN_LIVE_WEB_COMPARISON") != "1" && os.Getenv("RUN_LIVE_WEB_SEARXNG_EVAL") != "1" {
		t.Skip("set RUN_LIVE_WEB_SEARXNG_EVAL=1 (or RUN_LIVE_WEB_COMPARISON=1) to call OpenAI and SearXNG; Tavily is blocked")
	}
	guard := installLiveTavilyBlock(t)
	config := noTavilyLiveSearchConfig(requiredLiveEnv(t, "SEARXNG_BASE_URL"))
	if _, err := newNoTavilyLiveSearcher(config); err != nil {
		t.Fatalf("validate SearXNG-only configuration: %v", err)
	}
	key := requiredLiveEnv(t, "OPENAI_API_KEY")
	routerModel := requiredLiveEnv(t, "OPENAI_ROUTER_MODEL")
	agentModel := requiredLiveEnv(t, "OPENAI_AGENT_MODEL")
	classify, err := NewClassifier(key, routerModel)
	if err != nil {
		t.Fatal(err)
	}

	report := struct {
		StartedAt                        string                        `json:"started_at"`
		RouterModel                      string                        `json:"router_model"`
		AgentModel                       string                        `json:"agent_model"`
		PlanningReasoningEffort          string                        `json:"planning_reasoning_effort"`
		SynthesisReasoningEffort         string                        `json:"synthesis_reasoning_effort"`
		SynthesisMaxOutputTokens         int                           `json:"synthesis_max_output_tokens"`
		SynthesisReasoningScope          string                        `json:"synthesis_reasoning_scope"`
		SynthesisReasoningModels         []string                      `json:"synthesis_reasoning_models"`
		ReviewReasoningEffort            string                        `json:"review_reasoning_effort"`
		ReviewMaxOutputTokens            int                           `json:"review_max_output_tokens"`
		WebDraftReviewEnabled            bool                          `json:"web_draft_review_enabled"`
		WebDraftReviewMaximumPasses      int                           `json:"web_draft_review_maximum_passes"`
		WebDraftReviewScope              string                        `json:"web_draft_review_scope"`
		WebFinalRoundSynthesisOnly       bool                          `json:"web_final_round_synthesis_only"`
		SourceBoundReviewEnabled         bool                          `json:"source_bound_review_enabled"`
		SourceBoundReviewMaxOutputTokens int                           `json:"source_bound_review_max_output_tokens"`
		SourceBoundReviewMaxRepairs      int                           `json:"source_bound_review_max_repairs"`
		SourceBoundReviewScope           string                        `json:"source_bound_review_scope"`
		Configuration                    liveSearchSafetyConfiguration `json:"configuration"`
		TavilyNetworkAttempts            int64                         `json:"tavily_network_attempts"`
		ZeroTavilyAttemptsVerified       bool                          `json:"zero_tavily_attempts_verified"`
		Runs                             []liveComparisonRun           `json:"runs"`
	}{StartedAt: time.Now().UTC().Format(time.RFC3339), RouterModel: routerModel, AgentModel: agentModel,
		PlanningReasoningEffort: "omitted", SynthesisReasoningEffort: "omitted", ReviewReasoningEffort: "omitted",
		SynthesisReasoningModels: []string{"gpt-5.4-mini", "gpt-5.4-mini-2026-03-17"},
		WebDraftReviewEnabled:    true, WebDraftReviewMaximumPasses: 1,
		WebDraftReviewScope:         "One bounded model review after the first text draft following search_web. Post-search reasoning remains low for listed models; ordinary synthesis uses 2048 tokens, and source-bound review uses 3072. The review may request verification before the existing tool-round limit; the final already-budgeted round disables tools. Non-web turns are unchanged; this is a configured policy, not a correctness guarantee.",
		SynthesisReasoningScope:     "Low reasoning for the listed models after a search_web attempt. Ordinary post-search requests use 2048 output tokens; source-bound review/finalization uses 3072. First planning, non-web requests, and other models retain omitted reasoning/token settings. This is configured policy, not per-request telemetry.",
		WebFinalRoundSynthesisOnly:  true,
		SourceBoundReviewMaxRepairs: 1,
		SourceBoundReviewScope:      "Requires actual collected SearXNG source rows and an exact supported mini model at review/final-cap stage. Strict JSON claims bind quotes/numbers and temporal fields to exact source URLs; renderer adds hostnames and retains whole claims within 420 runes. At most one correction uses remaining existing round budget. These mechanical checks are not semantic entailment proof; evidence_reviews records candidates, rejections and rendered outputs.",
		Configuration: liveSearchSafetyConfiguration{Provider: config.Provider, SearXNGBaseURL: config.SearXNGBaseURL,
			TavilyFallback: false, ResearchExtraction: config.ResearchExtraction, TavilyProviderNetworkBlocked: true,
			TavilyGuardScope: "Tavily provider requests blocked by the default HTTP transport; initial and redirected page extraction URLs blocked by a shared pre-DNS policy. Not a machine-wide firewall."}}
	if agentModel == "gpt-5.4-mini" || agentModel == "gpt-5.4-mini-2026-03-17" {
		report.SynthesisReasoningEffort = "low"
		report.SynthesisMaxOutputTokens = 2048
		report.ReviewReasoningEffort = "low"
		report.ReviewMaxOutputTokens = 2048
		report.SourceBoundReviewEnabled = true
		report.SourceBoundReviewMaxOutputTokens = 3072
	}
	// Refuse to overwrite an earlier comparison, including if this run fails.
	if path := os.Getenv("LIVE_WEB_COMPARISON_REPORT"); path != "" {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatalf("create comparison report: %v", err)
		}
		t.Cleanup(func() {
			defer file.Close()
			report.TavilyNetworkAttempts = guard.attempts.Load()
			report.ZeroTavilyAttemptsVerified = report.TavilyNetworkAttempts == 0
			encoder := json.NewEncoder(file)
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(report); err != nil {
				t.Errorf("write comparison report: %v", err)
			}
		})
	}

	for _, scenario := range liveEyesWebScenarios() {
		for _, arm := range []string{"searxng_only"} {
			t.Run(scenario.name+"/"+arm, func(t *testing.T) {
				attemptsBefore := guard.attempts.Load()
				searcher, err := newNoTavilyLiveSearcher(config)
				if err != nil {
					t.Fatal(err)
				}
				recorded := &comparisonSearchTool{delegate: searcher}
				registry := tool.NewRegistry()
				if err := registry.Register(recorded); err != nil {
					t.Fatal(err)
				}
				executor, err := tool.NewExecutor(registry)
				if err != nil {
					t.Fatal(err)
				}
				agent, err := NewAgent(key, agentModel, registry, executor)
				if err != nil {
					t.Fatal(err)
				}
				evidenceReviews := make([]webEvidenceReviewRecord, 0)
				agent.onWebEvidenceReview = func(record webEvidenceReviewRecord) {
					record.Rejected = append([]string(nil), record.Rejected...)
					evidenceReviews = append(evidenceReviews, record)
				}
				service, err := assistant.NewService(assistant.NewRouter(classify), agent,
					&scenarioMemoryReader{cards: scenario.memories}, &emptyConversationReader{}, &noProposalConfirmer{})
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				started := time.Now()
				outcome, runErr := service.HandleUtterance(ctx, tool.Scope{
					UserID:    "00000000-0000-0000-0000-000000000001",
					SessionID: "00000000-0000-0000-0000-000000000002",
					TimeZone:  "America/Los_Angeles",
				}, "00000000-0000-0000-0000-000000000003", scenario.spoken)
				run := liveComparisonRun{Scenario: scenario.name, Arm: arm, Spoken: scenario.spoken,
					ElapsedMS: time.Since(started).Milliseconds(), Action: string(outcome.Decision.Action),
					Response: outcome.Response, Searches: recorded.snapshot(), EvidenceReviews: evidenceReviews}
				run.Quality = evaluateLiveSearchQuality(scenario, run.Response, run.Searches, time.Now())
				if runErr != nil {
					run.Error = runErr.Error()
					t.Errorf("HandleUtterance: %v", runErr)
				}
				// Capture outputs before assertions, including failed routing/search plans.
				defer func() {
					run.TavilyNetworkAttempts = guard.attempts.Load() - attemptsBefore
					run.ZeroTavilyAttemptsVerified = run.TavilyNetworkAttempts == 0
					if !run.ZeroTavilyAttemptsVerified {
						t.Errorf("Tavily network guard blocked %d forbidden request(s)", run.TavilyNetworkAttempts)
					}
					run.AssertionsPassed = !t.Failed()
					report.Runs = append(report.Runs, run)
					encoded, _ := json.Marshal(run)
					t.Logf("COMPARISON %s", encoded)
				}()
				if run.Action != string(assistant.ActionRespond) {
					t.Errorf("router action = %q, want respond", run.Action)
				}
				if strings.TrimSpace(run.Response) == "" {
					t.Error("Eyes response is empty")
				}
				for _, check := range run.Quality.Checks {
					if !check.Passed {
						t.Errorf("search quality gate failed: %+v", check)
					}
				}
				if len(run.Searches) == 0 {
					t.Fatal("Eyes did not call search_web")
				}
				calls := make([]json.RawMessage, 0, len(run.Searches))
				for _, search := range run.Searches {
					calls = append(calls, search.Arguments)
					assertNoTavilySearchResult(t, search.Content)
				}
				run.PlanAssertionsPassed = t.Run("search_plan", func(t *testing.T) {
					assertLiveSearchPlan(t, calls, scenario)
				})
			})
		}
	}
}

type liveComparisonRun struct {
	Scenario                   string                    `json:"scenario"`
	Arm                        string                    `json:"arm"`
	Spoken                     string                    `json:"spoken"`
	ElapsedMS                  int64                     `json:"elapsed_ms"`
	Action                     string                    `json:"action"`
	Response                   string                    `json:"response"`
	Error                      string                    `json:"error,omitempty"`
	AssertionsPassed           bool                      `json:"assertions_passed"`
	PlanAssertionsPassed       bool                      `json:"plan_assertions_passed"`
	TavilyNetworkAttempts      int64                     `json:"tavily_network_attempts"`
	ZeroTavilyAttemptsVerified bool                      `json:"zero_tavily_attempts_verified"`
	Quality                    liveQualityAssessment     `json:"quality"`
	Searches                   []liveComparisonSearch    `json:"searches"`
	EvidenceReviews            []webEvidenceReviewRecord `json:"evidence_reviews"`
}

type liveComparisonSearch struct {
	Arguments json.RawMessage `json:"arguments"`
	ElapsedMS int64           `json:"elapsed_ms"`
	Content   string          `json:"content"`
	Error     string          `json:"error,omitempty"`
}

type comparisonSearchTool struct {
	delegate tool.Tool
	mu       sync.Mutex
	searches []liveComparisonSearch
}

func (r *comparisonSearchTool) Spec() tool.Spec { return r.delegate.Spec() }

func (r *comparisonSearchTool) Execute(ctx context.Context, scope tool.Scope, arguments json.RawMessage) (tool.Result, error) {
	started := time.Now()
	search := liveComparisonSearch{Arguments: append(json.RawMessage(nil), arguments...)}
	// Reserve a slot before execution so concurrent calls retain invocation order.
	r.mu.Lock()
	index := len(r.searches)
	r.searches = append(r.searches, search)
	r.mu.Unlock()
	result, err := r.delegate.Execute(ctx, scope, arguments)
	search.ElapsedMS = time.Since(started).Milliseconds()
	search.Content = result.Content
	if err != nil {
		search.Error = err.Error()
	}
	r.mu.Lock()
	r.searches[index] = search
	r.mu.Unlock()
	return result, err
}

func (r *comparisonSearchTool) snapshot() []liveComparisonSearch {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]liveComparisonSearch(nil), r.searches...)
}
