package openai

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func parityTestRun(t *testing.T, scenario, arm, provider, query string, rows []any) map[string]any {
	t.Helper()
	payload := map[string]any{"provider": provider, "results": rows, "query": query, "credits": 0, "request_id": "REQUEST_ID_SENTINEL"}
	if provider == "" {
		delete(payload, "provider")
	}
	content, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"scenario": scenario, "arm": arm, "spoken": "Find Aurora telescope facts", "response": "PRIOR_ANSWER_SENTINEL", "quality": map[string]any{"verdict": "GOLD_SENTINEL"}, "evidence_reviews": []any{map[string]any{"candidate_json": "CANDIDATE_SENTINEL"}},
		"searches": []any{map[string]any{"arguments": map[string]any{"query": query, "include_domains": []string{}}, "content": string(content)}}}
}

func parityTestData(t *testing.T, runs ...map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"started_at": "2026-09-04T04:00:00Z", "runs": runs, "response": "ROOT_ANSWER_SENTINEL"})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func parityTestSource(endpoint, snippet string) map[string]any {
	return map[string]any{"url": endpoint, "title": "Aurora telescope", "snippet": snippet, "published_date": "2026-09-03", "score": 0.7}
}

func TestProviderParityRejectsContaminationAndNeverUsesMixedFallbackArms(t *testing.T) {
	t.Parallel()
	good := parityTestSource("https://aurora.example/current", "Current source fact.")
	foreign := parityTestSource("https://foreign.example/leak", "FOREIGN_SOURCE_SENTINEL")
	for _, scenario := range []struct{ name, actual string }{{"foreign_provider", "searxng"}, {"missing_provider", ""}} {
		t.Run(scenario.name, func(t *testing.T) {
			run := parityTestRun(t, "aurora", "tavily", "tavily", "Aurora", []any{good})
			bad := parityTestRun(t, "aurora", "tavily", scenario.actual, "More Aurora", []any{foreign})
			run["searches"] = append(run["searches"].([]any), bad["searches"].([]any)...)
			capture, err := loadProviderParityCapture(strings.NewReader(parityTestData(t, run)), "fixture.json", "tavily", "tavily", []string{"aurora"})
			if err != nil {
				t.Fatal(err)
			}
			got := capture.Scenarios[0]
			if got.ProviderEligible || len(got.Sources) != 0 || got.UnverifiedSearchCount != 1 || len(got.ExclusionReasons) == 0 || got.Searches[1].ActualProvider != scenario.actual {
				t.Errorf("contaminated pure arm was admitted: %#v", got)
			}
		})
	}
	selected := parityTestRun(t, "aurora", "tavily", "tavily", "Aurora", []any{good})
	mixed := parityTestRun(t, "aurora", "searxng_fallback", "tavily", "Aurora fallback", []any{foreign})
	capture, err := loadProviderParityCapture(strings.NewReader(parityTestData(t, mixed, selected)), "fixture.json", "tavily", "tavily", []string{"aurora"})
	if err != nil {
		t.Fatal(err)
	}
	if !capture.Scenarios[0].ProviderEligible || len(capture.Scenarios[0].Sources) != 1 || capture.Scenarios[0].Sources[0].URL != good["url"] || len(capture.IgnoredRuns) != 1 || !reflect.DeepEqual(capture.IgnoredRuns[0].ActualProviders, []string{"tavily"}) {
		t.Errorf("fallback arm was merged into the pure baseline: %#v", capture)
	}
	if _, err := loadProviderParityCapture(strings.NewReader(parityTestData(t, mixed)), "fixture.json", "tavily", "searxng_fallback", nil); err == nil {
		t.Error("loader allowed a mixed/fallback arm to be selected as pure baseline")
	}
}

func TestProviderParityMalformedRowsAreCountedAndExcluded(t *testing.T) {
	t.Parallel()
	rows := []any{
		parityTestSource("https://aurora.example/valid", "Source evidence."), nil, "not an object",
		map[string]any{"url": "javascript:alert(1)", "snippet": "Bad URL"},
		map[string]any{"url": "https://fake:credential@aurora.example/info", "snippet": "Credential URL"},
		map[string]any{"url": "https://aurora.example/empty"},
		map[string]any{"url": "https://aurora.example/wrongtype", "snippet": 8},
		map[string]any{"url": "https://aurora.example/title-only", "title": "Title-only evidence"},
	}
	input := parityTestData(t, parityTestRun(t, "aurora", "searxng_only", "searxng", "Aurora", rows))
	capture, err := loadProviderParityCapture(strings.NewReader(input), "fixture.json", "searxng", "searxng_only", []string{"aurora"})
	if err != nil {
		t.Fatal(err)
	}
	got := capture.Scenarios[0]
	if got.ResultOccurrences != 8 || got.UsableResultOccurrences != 2 || got.MalformedRowCount != 6 || len(got.Sources) != 2 || !got.ProviderEligible {
		t.Errorf("malformed source accounting=%#v", got)
	}
	if got.Metadata.WithTitle != 2 || got.Metadata.WithSnippet != 1 || got.Metadata.WithPublicationDate != 1 || got.Metadata.ParseableDate != 1 {
		t.Errorf("source metadata counts=%#v", got.Metadata)
	}
}

func TestProviderParityMissingFailedAndEmptySearchesStayExplicit(t *testing.T) {
	t.Parallel()
	failed := map[string]any{"scenario": "failed", "arm": "searxng_only", "searches": []any{map[string]any{"arguments": map[string]any{"query": "Aurora"}, "content": "", "error": "captured upstream unavailable"}}}
	noSearch := map[string]any{"scenario": "no_search", "arm": "searxng_only", "searches": []any{}}
	empty := parityTestRun(t, "empty", "searxng_only", "searxng", "Aurora", []any{})
	capture, err := loadProviderParityCapture(strings.NewReader(parityTestData(t, failed, noSearch, empty)), "fixture.json", "searxng", "searxng_only", []string{"missing", "failed", "no_search", "empty"})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]parityScenarioCapture{}
	for _, row := range capture.Scenarios {
		byName[row.Scenario] = row
	}
	if !byName["missing"].Missing || byName["missing"].ProviderEligible || !byName["no_search"].NoSearchesRecorded || byName["failed"].FailedSearchCount != 1 || byName["failed"].UnverifiedSearchCount != 1 || byName["empty"].EmptySearchCount != 1 {
		t.Errorf("missing, failed and empty searches were conflated: %#v", byName)
	}
	comparison := compareProviderParity(parityCapture{}, capture)
	for _, row := range comparison.Scenarios {
		if !row.BaselineMissing || row.SourceDiagnosticsUsable || row.QuerySequencesIdentical {
			t.Errorf("missing baseline treated as zero-result parity: %#v", row)
		}
	}
}

func TestProviderParitySourcesPreserveDistinctVersionsExcludeAnswersAndOrderStably(t *testing.T) {
	t.Parallel()
	first := parityTestSource("https://aurora.example/info", "星 telescope opened.")
	second := parityTestSource("https://aurora.example/info", "Later telescope hours are 8am to 5pm.")
	second["published_date"] = "2026-09-04T02:00:00Z"
	duplicate := parityTestSource("https://aurora.example/info", "星 telescope opened.")
	duplicate["score"] = 0.1 // Retrieval score changes do not create a new source-text version.
	input := parityTestData(t, parityTestRun(t, "zeta", "tavily", "tavily", "Aurora", []any{second, first, duplicate}), parityTestRun(t, "alpha", "tavily", "tavily", "Other", []any{}))
	var previous string
	for iteration := 0; iteration < 10; iteration++ {
		capture, err := loadProviderParityCapture(strings.NewReader(input), "fixture.json", "tavily", "tavily", []string{"zeta", "alpha"})
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(capture)
		if strings.Contains(string(encoded), "SENTINEL") || (iteration > 0 && string(encoded) != previous) {
			t.Error("past answer/request/candidate leaked or repeated loading changed ordering")
		}
		previous = string(encoded)
		if capture.Scenarios[0].Scenario != "alpha" || capture.Scenarios[1].DistinctSourceRows != 2 || capture.Scenarios[1].ResultOccurrences != 3 || len(capture.Scenarios[1].URLs) != 1 {
			t.Errorf("source version/stable scenario accounting=%#v", capture.Scenarios)
		}
		var occurrences int
		for _, row := range capture.Scenarios[1].Sources {
			occurrences += len(row.Occurrences)
		}
		if occurrences != 3 {
			t.Errorf("source occurrence provenance lost: %d", occurrences)
		}
		fixture := paritySourceFixture(capture.Scenarios[1].Sources)
		fixtureJSON, _ := json.Marshal(fixture)
		if strings.Contains(string(fixtureJSON), "score") || strings.Contains(string(fixtureJSON), "occurrences") || strings.Contains(string(fixtureJSON), "query") {
			t.Error("source-only replay fixture contains retrieval metadata")
		}
		if capture.Scenarios[1].DistinctSourceJSONBytes != len(fixtureJSON) || capture.Scenarios[1].DistinctSourceTextBytes <= 0 {
			t.Error("distinct source byte accounting does not describe the source-only fixture")
		}
	}
}

func TestProviderParityOverlapDoesNotConflateDomainsURLsOrEquivalentCoverage(t *testing.T) {
	t.Parallel()
	load := func(provider, arm, endpoint, snippet string) parityCapture {
		capture, err := loadProviderParityCapture(strings.NewReader(parityTestData(t, parityTestRun(t, "aurora", arm, provider, "Aurora", []any{parityTestSource(endpoint, snippet)}))), "fixture.json", provider, arm, []string{"aurora"})
		if err != nil {
			t.Fatal(err)
		}
		return capture
	}
	baseline := load("tavily", "tavily", "https://publisher.example/one", "Aurora aperture is 40 cm.")
	for _, scenario := range []struct {
		name, endpoint, snippet string
		urls, domains           int
	}{
		{"same_host_different_article", "https://publisher.example/two", "Aurora aperture is 40 cm.", 0, 1},
		{"different_host_same_fact", "https://official.example/info", "Aurora aperture is 40 cm.", 0, 0},
		{"same_url_different_fact", "https://publisher.example/one", "The page now lists cafe hours only.", 1, 1},
		{"subdomain_not_identical_host", "https://news.publisher.example/one", "Aurora aperture is 40 cm.", 0, 0},
		{"hostname_suffix_attack", "https://publisher.example.evil.test/one", "Aurora aperture is 40 cm.", 0, 0},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			comparison := compareProviderParity(baseline, load("searxng", "searxng_only", scenario.endpoint, scenario.snippet)).Scenarios[0]
			if len(comparison.SharedURLs) != scenario.urls || len(comparison.SharedDomains) != scenario.domains || !comparison.SourceDiagnosticsUsable || len(comparison.Caveats) == 0 {
				t.Errorf("overlap treated as source equivalence: %#v", comparison)
			}
		})
	}
}

func TestProviderParityTimestampAndQueryDifferencesAreReported(t *testing.T) {
	t.Parallel()
	row := parityTestSource("https://aurora.example/info", "Source fact.")
	baseline, err := loadProviderParityCapture(strings.NewReader(parityTestData(t, parityTestRun(t, "aurora", "tavily", "tavily", "Original query", []any{row}))), "baseline.json", "tavily", "tavily", nil)
	if err != nil {
		t.Fatal(err)
	}
	candidateData := strings.Replace(parityTestData(t, parityTestRun(t, "aurora", "searxng_only", "searxng", "Targeted follow-up query", []any{row})), "2026-09-04T04:00:00Z", "2026-09-04T06:30:00Z", 1)
	candidate, err := loadProviderParityCapture(strings.NewReader(candidateData), "candidate.json", "searxng", "searxng_only", nil)
	if err != nil {
		t.Fatal(err)
	}
	comparison := compareProviderParity(baseline, candidate)
	if comparison.ReportStartsIdentical || comparison.ReportStartDeltaSeconds != 9000 || comparison.Scenarios[0].QuerySequencesIdentical || comparison.Scenarios[0].ArgumentSequencesIdentical || len(comparison.Scenarios[0].Caveats) < 3 {
		t.Errorf("uncontrolled timestamp/query differences hidden: %#v", comparison)
	}
}

func TestProviderParityRejectsDuplicateScenarioRunsAndMissingTime(t *testing.T) {
	t.Parallel()
	run := parityTestRun(t, "aurora", "tavily", "tavily", "Aurora", []any{})
	for _, input := range []string{parityTestData(t, run, run), `{"runs":[]}`, `{"started_at":"2026-09-04T04:00:00","runs":[]}`} {
		if _, err := loadProviderParityCapture(strings.NewReader(input), "fixture.json", "tavily", "tavily", nil); err == nil {
			t.Error("ambiguous duplicate scenario/time accepted")
		}
	}
}
