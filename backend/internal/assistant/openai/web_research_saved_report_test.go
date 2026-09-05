package openai

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestReviewSavedWebResearchReport regrades an existing report without any
// network requests, credentials, inference, or report mutation. In old reports
// the run clock is approximated by started_at plus cumulative elapsed_ms. Raw
// captured evidence remains the source of truth for subsequent manual review.
func TestReviewSavedWebResearchReport(t *testing.T) {
	path := os.Getenv("LIVE_WEB_QUALITY_REVIEW_REPORT")
	if path == "" {
		t.Skip("set LIVE_WEB_QUALITY_REVIEW_REPORT to audit saved evidence offline")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		StartedAt    string `json:"started_at"`
		EvidenceAsOf string `json:"evidence_as_of"`
		Mode         string `json:"mode"`
		Runs         []struct {
			liveComparisonRun
			CapturedSearches []liveComparisonSearch `json:"captured_searches"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	clockText := report.StartedAt
	isReplay := report.Mode == "captured_fixed_corpus_synthesis"
	if report.Mode != "" && !isReplay {
		t.Fatalf("unsupported report mode %q; do not guess replay semantics", report.Mode)
	}
	if isReplay {
		clockText = report.EvidenceAsOf
		t.Log("Offline regrade of captured fixed-corpus synthesis: frozen evidence_as_of, captured_searches only; no live retrieval success implied.")
	}
	asOf, err := time.Parse(time.RFC3339, clockText)
	if err != nil {
		t.Fatalf("saved report must have an explicit started_at (or replay evidence_as_of) clock: %v", err)
	}
	if len(report.Runs) == 0 {
		t.Fatal("saved report has no runs")
	}
	for _, run := range report.Runs {
		searches := run.Searches
		if isReplay {
			searches = run.CapturedSearches
		} else {
			asOf = asOf.Add(time.Duration(run.ElapsedMS) * time.Millisecond)
		}
		t.Run(run.Scenario+"/"+run.Arm, func(t *testing.T) {
			quality := evaluateLiveSearchQuality(liveEyesWebScenario{name: run.Scenario}, run.Response, searches, asOf)
			for _, check := range quality.Checks {
				if !check.Passed {
					t.Errorf("%s: %s", check.Name, check.Detail)
				}
			}
			for _, manual := range quality.ManualReviewRequired {
				t.Logf("manual review required: %s", manual)
			}
		})
	}
}
