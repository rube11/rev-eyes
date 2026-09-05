package openai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rube11/rev-eyes/backend/internal/assistant"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

// Deliberately decode only the runnable inputs. Rubric fields, expected facts,
// validation links and labels must never be presented to the answering model.
type heldoutWebCase struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	TimeZone string `json:"time_zone"`
	AsOf     string `json:"as_of"`
}

func loadHeldoutWebCases(reader io.Reader) ([]heldoutWebCase, string, error) {
	data, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, "", errors.New("held-out manifest cannot be read or exceeds 1 MiB")
	}
	var manifest struct {
		Cases []heldoutWebCase `json:"cases"`
	}
	if json.Unmarshal(data, &manifest) != nil || len(manifest.Cases) == 0 || len(manifest.Cases) > 20 {
		return nil, "", errors.New("held-out manifest must contain 1-20 valid cases")
	}
	seen := make(map[string]bool)
	for _, item := range manifest.Cases {
		if item.ID == "" || len(item.ID) > 100 || strings.ContainsAny(item.ID, " /\\\t\r\n") || seen[item.ID] ||
			strings.TrimSpace(item.Question) == "" || utf8.RuneCountInString(item.Question) > 1600 {
			return nil, "", errors.New("held-out case requires a unique simple ID and bounded question")
		}
		seen[item.ID] = true
		asOf, err := time.Parse(time.RFC3339, item.AsOf)
		if err != nil {
			return nil, "", errors.New("held-out as_of must be offset-qualified RFC3339")
		}
		if item.TimeZone == "" || item.TimeZone == "Local" {
			return nil, "", errors.New("held-out time_zone must be explicit")
		}
		location, err := time.LoadLocation(item.TimeZone)
		if err != nil {
			return nil, "", errors.New("held-out time_zone is not recognized")
		}
		_, offset := asOf.Zone()
		_, localOffset := asOf.In(location).Zone()
		if offset != localOffset {
			return nil, "", errors.New("held-out as_of offset disagrees with time_zone")
		}
	}
	digest := sha256.Sum256(data)
	return manifest.Cases, hex.EncodeToString(digest[:]), nil
}

// This is a full SearXNG-only pilot through routing, search and answer generation.
// It spends OpenAI credits only with an explicit opt-in. Execution assertions
// are NOT quality scores: independent review against the frozen manifest is
// required. The expected facts and validation sources never enter model input.
func TestLiveEyesHeldoutWebPilot(t *testing.T) {
	if os.Getenv("RUN_LIVE_WEB_HELDOUT") != "1" {
		t.Skip("opt in with RUN_LIVE_WEB_HELDOUT=1; spends OpenAI credits, Tavily blocked")
	}
	manifestPath, err := filepath.Abs(requiredLiveEnv(t, "LIVE_WEB_HELDOUT_MANIFEST"))
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	cases, digest, err := loadHeldoutWebCases(file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if expected := strings.ToLower(requiredLiveEnv(t, "LIVE_WEB_HELDOUT_SHA256")); expected != digest {
		t.Fatal("held-out manifest does not match the pre-approved SHA256; no model call was made")
	}
	guard := installLiveTavilyBlock(t)
	config := noTavilyLiveSearchConfig(requiredLiveEnv(t, "SEARXNG_BASE_URL"))
	if _, err := newNoTavilyLiveSearcher(config); err != nil {
		t.Fatal(err)
	}
	key := requiredLiveEnv(t, "OPENAI_API_KEY")
	routerModel, agentModel := requiredLiveEnv(t, "OPENAI_ROUTER_MODEL"), requiredLiveEnv(t, "OPENAI_AGENT_MODEL")
	classifier, err := NewClassifier(key, routerModel)
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.OpenFile(requiredLiveEnv(t, "LIVE_WEB_HELDOUT_REPORT"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	type pilotRun struct {
		Case                 heldoutWebCase            `json:"case"`
		StartedAt            string                    `json:"started_at"`
		Action               string                    `json:"action"`
		RoutedQuery          string                    `json:"routed_query"`
		Response             string                    `json:"response"`
		Error                string                    `json:"error,omitempty"`
		ElapsedMS            int64                     `json:"elapsed_ms"`
		ExecutionPassed      bool                      `json:"execution_checks_passed"`
		ResponseRunes        int                       `json:"response_runes"`
		TavilyAttempts       int64                     `json:"tavily_network_attempts"`
		UnnormalizedAttempts int64                     `json:"unnormalized_host_attempts"`
		Searches             []liveComparisonSearch    `json:"searches"`
		EvidenceReviews      []webEvidenceReviewRecord `json:"evidence_reviews"`
	}
	report := struct {
		StartedAt            string     `json:"started_at"`
		Mode                 string     `json:"mode"`
		Manifest             string     `json:"manifest"`
		ManifestSHA256       string     `json:"manifest_sha256"`
		RouterModel          string     `json:"router_model"`
		AgentModel           string     `json:"agent_model"`
		SearXNGBaseURL       string     `json:"searxng_base_url"`
		TavilyFallback       bool       `json:"tavily_fallback"`
		TavilyAttempts       int64      `json:"tavily_network_attempts"`
		UnnormalizedAttempts int64      `json:"unnormalized_host_attempts"`
		ZeroTavilyVerified   bool       `json:"zero_tavily_attempts_verified"`
		ManualReviewRequired bool       `json:"manual_review_required"`
		Scope                string     `json:"scope"`
		Runs                 []pilotRun `json:"runs"`
	}{StartedAt: time.Now().UTC().Format(time.RFC3339), Mode: "full_stack_searxng_only_heldout_pilot", Manifest: manifestPath, ManifestSHA256: digest,
		RouterModel: routerModel, AgentModel: agentModel, SearXNGBaseURL: config.SearXNGBaseURL, ManualReviewRequired: true,
		Scope: "Frozen synthetic questions and as_of are supplied to the existing router/agent with empty synthetic memory. Live search/extraction happens at actual run time and cannot replay the historical web. Evaluation facts, source hints, scores and prior answers are not supplied. Checks only verify execution and provider safety, not answer quality, truth or Tavily parity. Dispatched search attempts, their errors, and reviewed drafts are retained. Executor pre-dispatch rejections and individual failed page fetches are not separately captured."}
	t.Cleanup(func() {
		defer output.Close()
		report.TavilyAttempts = guard.attempts.Load()
		report.UnnormalizedAttempts = guard.unnormalizedAttempts.Load()
		report.ZeroTavilyVerified = report.TavilyAttempts == 0
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			t.Errorf("write held-out pilot: %v", err)
		}
	})
	for _, item := range cases {
		t.Run(item.ID, func(t *testing.T) {
			run := pilotRun{Case: item, StartedAt: time.Now().UTC().Format(time.RFC3339), Searches: []liveComparisonSearch{}, EvidenceReviews: []webEvidenceReviewRecord{}}
			attemptsBefore := guard.attempts.Load()
			unnormalizedBefore := guard.unnormalizedAttempts.Load()
			defer func() {
				run.ResponseRunes = utf8.RuneCountInString(run.Response)
				run.TavilyAttempts = guard.attempts.Load() - attemptsBefore
				run.UnnormalizedAttempts = guard.unnormalizedAttempts.Load() - unnormalizedBefore
				if run.UnnormalizedAttempts != 0 {
					t.Error("pilot attempted an unnormalized Unicode hostname; evaluation requires ASCII or punycode destinations")
				}
				if issue := heldoutWebExecutionIssue(run.Response, run.TavilyAttempts); issue != "" {
					t.Error(issue)
				}
				run.ExecutionPassed = !t.Failed()
				report.Runs = append(report.Runs, run)
				t.Logf("pilot case=%s execution_passed=%t elapsed_ms=%d", item.ID, run.ExecutionPassed, run.ElapsedMS)
			}()
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
			asOf, _ := time.Parse(time.RFC3339, item.AsOf)
			agent.now = func() time.Time { return asOf }
			agent.onWebEvidenceReview = func(review webEvidenceReviewRecord) { run.EvidenceReviews = append(run.EvidenceReviews, review) }
			service, err := assistant.NewService(assistant.NewRouter(classifier), agent,
				&scenarioMemoryReader{}, &emptyConversationReader{}, &noProposalConfirmer{})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			started := time.Now()
			outcome, runErr := service.HandleUtterance(ctx, tool.Scope{UserID: "00000000-0000-0000-0000-000000000001", SessionID: "00000000-0000-0000-0000-000000000002", TimeZone: item.TimeZone}, "00000000-0000-0000-0000-000000000003", item.Question)
			run.ElapsedMS, run.Action, run.Response = time.Since(started).Milliseconds(), string(outcome.Decision.Action), outcome.Response
			run.RoutedQuery = outcome.Decision.Query
			run.Searches = recorded.snapshot()
			if runErr != nil {
				run.Error = runErr.Error()
				t.Errorf("pilot execution: %v", runErr)
			}
			if run.Action != string(assistant.ActionRespond) || strings.TrimSpace(run.Response) == "" {
				t.Error("pilot did not produce a respond action with text")
			}
			if len(run.Searches) == 0 {
				t.Error("research pilot performed no search")
			}
			for _, search := range run.Searches {
				assertNoTavilySearchResult(t, search.Content)
			}
		})
	}
}

func heldoutWebExecutionIssue(response string, tavilyAttempts int64) string {
	if tavilyAttempts != 0 {
		return "pilot attempted Tavily access"
	}
	if strings.TrimSpace(response) == "" {
		return "pilot response is empty"
	}
	if utf8.RuneCountInString(response) > 420 {
		return "pilot response exceeds the 420-Unicode-character contract"
	}
	return ""
}

func TestHeldoutWebManifestExcludesAnswersRubricsAndSourceHints(t *testing.T) {
	const manifest = `{"cases":[{"id":"new_case","question":"Find current public details.","time_zone":"America/Los_Angeles","as_of":"2026-09-04T18:00:00-07:00","expected_facts":["SECRET RUBRIC"],"primary_source_validation":"https://hint.example","answer":"FORGED ANSWER"}]}`
	cases, digest, err := loadHeldoutWebCases(strings.NewReader(manifest))
	if err != nil || len(cases) != 1 || len(digest) != 64 {
		t.Fatalf("load: %v cases=%v digest=%s", err, cases, digest)
	}
	encoded, _ := json.Marshal(cases)
	for _, leak := range []string{"SECRET RUBRIC", "hint.example", "FORGED ANSWER", "expected_facts"} {
		if strings.Contains(string(encoded), leak) {
			t.Errorf("model input contains grading information: %s", leak)
		}
	}
}

func TestHeldoutWebManifestRejectsUnsafeOrAmbiguousInputs(t *testing.T) {
	valid := `{"id":"new_case","question":"Find current public details.","time_zone":"America/Los_Angeles","as_of":"2026-09-04T18:00:00-07:00"}`
	for _, input := range []string{
		`{}`, `{"cases":[]}`, `{"cases":[` + valid + `,` + valid + `]}`,
		`{"cases":[` + strings.Replace(valid, "new_case", "../escape", 1) + `]}`,
		`{"cases":[` + strings.Replace(valid, "America/Los_Angeles", "Local", 1) + `]}`,
		`{"cases":[` + strings.Replace(valid, "-07:00", "Z", 1) + `]}`,
		`{"cases":[` + strings.Replace(valid, "Find current public details.", "", 1) + `]}`,
		strings.Repeat(" ", (1<<20)+1),
	} {
		if _, _, err := loadHeldoutWebCases(strings.NewReader(input)); err == nil {
			t.Error("invalid manifest accepted")
		}
	}
}
