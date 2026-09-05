package openai

// Captured provider parity is a retrieval diagnostic, never an answer-quality
// score. Inputs are historical source rows; prior model answers are not gold
// labels and are deliberately absent from the loader's input/output types.
import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

type parityInputReport struct {
	StartedAt string           `json:"started_at"`
	Runs      []parityInputRun `json:"runs"`
}

type parityInputRun struct {
	Scenario string                 `json:"scenario"`
	Arm      string                 `json:"arm"`
	Spoken   string                 `json:"spoken"`
	Error    string                 `json:"error"`
	Searches []liveComparisonSearch `json:"searches"`
}

type paritySourceOccurrence struct {
	SearchIndex int      `json:"search_index"`
	ResultIndex int      `json:"result_index"`
	Score       *float64 `json:"score,omitempty"`
}

type paritySourceRow struct {
	webEvidenceSource
	Occurrences []paritySourceOccurrence `json:"occurrences"`
}

type paritySearchAudit struct {
	Index             int                        `json:"index"`
	Arguments         json.RawMessage            `json:"arguments"`
	Query             string                     `json:"query"`
	PayloadQuery      string                     `json:"payload_query,omitempty"`
	ActualProvider    string                     `json:"actual_provider,omitempty"`
	ProviderVerified  bool                       `json:"provider_verified"`
	Failure           string                     `json:"failure,omitempty"`
	Issues            []string                   `json:"issues"`
	ElapsedMS         int64                      `json:"elapsed_ms"`
	PayloadBytes      int                        `json:"payload_bytes"`
	ResultCount       int                        `json:"result_count"`
	UsableResultCount int                        `json:"usable_result_count"`
	SourceTextBytes   int                        `json:"source_text_bytes"`
	MalformedRows     []string                   `json:"malformed_rows"`
	ProviderMetadata  map[string]json.RawMessage `json:"provider_metadata"`
}

type parityMetadataCounts struct {
	WithTitle               int `json:"with_title"`
	WithSnippet             int `json:"with_snippet"`
	WithPublicationDate     int `json:"with_publication_date"`
	ParseableDate           int `json:"parseable_publication_date"`
	WithPageExcerpts        int `json:"with_page_excerpts"`
	WithPagePublicationDate int `json:"with_page_publication_date"`
}

type parityScenarioCapture struct {
	Scenario                string               `json:"scenario"`
	Missing                 bool                 `json:"missing"`
	Spoken                  string               `json:"spoken,omitempty"`
	RunError                string               `json:"run_error,omitempty"`
	ProviderEligible        bool                 `json:"provider_eligible"`
	ExclusionReasons        []string             `json:"exclusion_reasons"`
	NoSearchesRecorded      bool                 `json:"no_searches_recorded"`
	SearchCount             int                  `json:"search_count"`
	FailedSearchCount       int                  `json:"failed_search_count"`
	EmptySearchCount        int                  `json:"empty_search_count"`
	UnverifiedSearchCount   int                  `json:"unverified_search_count"`
	MalformedRowCount       int                  `json:"malformed_row_count"`
	ResultOccurrences       int                  `json:"result_occurrences"`
	UsableResultOccurrences int                  `json:"usable_result_occurrences"`
	DistinctSourceRows      int                  `json:"distinct_source_rows"`
	DistinctSourceTextBytes int                  `json:"distinct_source_text_bytes"`
	DistinctSourceJSONBytes int                  `json:"distinct_source_json_bytes"`
	Metadata                parityMetadataCounts `json:"distinct_source_metadata"`
	URLs                    []string             `json:"diagnostic_canonical_urls"`
	Domains                 []string             `json:"diagnostic_exact_hostnames"`
	Searches                []paritySearchAudit  `json:"searches"`
	Sources                 []paritySourceRow    `json:"source_rows"`
}

type parityIgnoredRun struct {
	Scenario        string   `json:"scenario"`
	Arm             string   `json:"arm"`
	ActualProviders []string `json:"actual_providers"`
	PayloadIssues   []string `json:"payload_issues"`
	Reason          string   `json:"reason"`
}

type parityCapture struct {
	Path        string                  `json:"source_report"`
	SHA256      string                  `json:"source_report_sha256"`
	StartedAt   string                  `json:"report_started_at"`
	Provider    string                  `json:"expected_provider"`
	SelectedArm string                  `json:"selected_arm"`
	ClockScope  string                  `json:"clock_scope"`
	Scenarios   []parityScenarioCapture `json:"scenarios"`
	IgnoredRuns []parityIgnoredRun      `json:"ignored_runs"`
}

// URL identity is deliberately conservative: strip fragments and normalize
// scheme/hostname case and an empty root path. Keep path/query, www and other
// subdomains distinct. A shared hostname is not equivalent source coverage.
func parityURLIdentity(raw string) (canonical, host string, ok bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return "", "", false
	}
	host = strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if strings.ContainsAny(host, " \t\r\n") {
		return "", "", false
	}
	parsed.Host = strings.ToLower(strings.TrimSuffix(parsed.Host, "."))
	parsed.Fragment, parsed.RawFragment = "", ""
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String(), host, true
}

func paritySortedSet(values map[string]bool) []string {
	ordered := make([]string, 0, len(values))
	for value := range values {
		ordered = append(ordered, value)
	}
	sort.Strings(ordered)
	return ordered
}

// This whitelist is the only source fixture representation. It cannot include
// past answers, grading verdicts, candidate claims, request IDs, or tool calls.
func paritySourceFixture(rows []paritySourceRow) []webEvidenceSource {
	fixture := make([]webEvidenceSource, 0, len(rows))
	for _, row := range rows {
		fixture = append(fixture, row.webEvidenceSource)
	}
	return fixture
}

// Count observed text fields, including old captures that duplicated fetched
// text in Snippet. This is payload accounting, not an eligible-evidence budget.
func paritySourceTextBytes(source webEvidenceSource) int {
	return len(source.Title) + len(source.Snippet) + len(source.DiscoverySnippet) + len(strings.Join(source.PageExcerpts, ""))
}

func parityParseSearch(search liveComparisonSearch, index int, provider string) (paritySearchAudit, []paritySourceRow, bool) {
	audit := paritySearchAudit{Index: index, Arguments: append(json.RawMessage(nil), search.Arguments...), ElapsedMS: search.ElapsedMS,
		Failure: search.Error, PayloadBytes: len(search.Content), Issues: []string{}, MalformedRows: []string{}, ProviderMetadata: map[string]json.RawMessage{}}
	var args struct {
		Query string `json:"query"`
	}
	if json.Unmarshal(search.Arguments, &args) != nil || strings.TrimSpace(args.Query) == "" {
		audit.Issues = append(audit.Issues, "missing or malformed search arguments/query")
	}
	audit.Query = args.Query
	var payload map[string]json.RawMessage
	if strings.TrimSpace(search.Content) == "" {
		audit.Issues = append(audit.Issues, "no response payload; actual provider is unverified")
		return audit, nil, search.Error == "" // Failed requests do not manufacture provider identity.
	}
	if json.Unmarshal([]byte(search.Content), &payload) != nil || payload == nil {
		audit.Issues = append(audit.Issues, "malformed search response payload")
		return audit, nil, true
	}
	_ = json.Unmarshal(payload["provider"], &audit.ActualProvider)
	_ = json.Unmarshal(payload["query"], &audit.PayloadQuery)
	audit.ProviderVerified = audit.ActualProvider == provider
	contaminated := !audit.ProviderVerified
	if contaminated {
		audit.Issues = append(audit.Issues, "actual payload provider does not match the selected pure-provider arm")
	}
	for _, key := range []string{"mode", "topic", "recency", "response_time", "latency_ms", "credits", "responsive_engines", "unresponsive_engines", "extracted_results"} {
		if value, ok := payload[key]; ok {
			audit.ProviderMetadata[key] = append(json.RawMessage(nil), value...)
		}
	}
	var rawRows []json.RawMessage
	if !strings.HasPrefix(strings.TrimSpace(string(payload["results"])), "[") || json.Unmarshal(payload["results"], &rawRows) != nil {
		audit.Issues = append(audit.Issues, "results is not a source-row array")
		return audit, nil, contaminated
	}
	audit.ResultCount = len(rawRows)
	rows := make([]paritySourceRow, 0, len(rawRows))
	for resultIndex, raw := range rawRows {
		var row struct {
			webEvidenceSource
			Score *float64 `json:"score"`
		}
		if json.Unmarshal(raw, &row) != nil {
			audit.MalformedRows = append(audit.MalformedRows, fmt.Sprintf("row %d: malformed source field types", resultIndex))
			continue
		}
		if _, _, ok := parityURLIdentity(row.URL); !ok || strings.TrimSpace(row.Title+row.Snippet+row.DiscoverySnippet+strings.Join(row.PageExcerpts, "")) == "" {
			audit.MalformedRows = append(audit.MalformedRows, fmt.Sprintf("row %d: invalid public URL or empty source text", resultIndex))
			continue
		}
		audit.UsableResultCount++
		audit.SourceTextBytes += paritySourceTextBytes(row.webEvidenceSource)
		rows = append(rows, paritySourceRow{webEvidenceSource: row.webEvidenceSource,
			Occurrences: []paritySourceOccurrence{{SearchIndex: index, ResultIndex: resultIndex, Score: row.Score}}})
	}
	if search.Error != "" || contaminated {
		return audit, nil, contaminated // Never use content accompanying a failed/foreign-provider search as evidence.
	}
	return audit, rows, false
}

func loadProviderParityCapture(reader io.Reader, path, provider, arm string, expected []string) (parityCapture, error) {
	if (provider != "tavily" || arm != "tavily") && (provider != "searxng" || arm != "searxng_only") {
		return parityCapture{}, errors.New("parity loader only accepts explicit pure Tavily or SearXNG-only arms")
	}
	data, err := io.ReadAll(io.LimitReader(reader, capturedEvidenceLimit+1))
	if err != nil || len(data) > capturedEvidenceLimit {
		return parityCapture{}, errors.New("captured parity report is unreadable or exceeds 16 MiB")
	}
	var input parityInputReport
	if err := json.Unmarshal(data, &input); err != nil {
		return parityCapture{}, errors.New("captured parity report is malformed JSON")
	}
	if _, err := time.Parse(time.RFC3339, input.StartedAt); err != nil {
		return parityCapture{}, errors.New("captured parity report needs an offset-qualified started_at")
	}
	digest := sha256.Sum256(data)
	capture := parityCapture{Path: path, SHA256: hex.EncodeToString(digest[:]), StartedAt: input.StartedAt, Provider: provider, SelectedArm: arm,
		ClockScope: "Overall historical report start, not an individual search timestamp. Query timing, currentness and engine state may differ between captures.", IgnoredRuns: []parityIgnoredRun{}}
	selected := make(map[string]parityInputRun)
	names := make(map[string]bool)
	for _, name := range expected {
		names[name] = true
	}
	for _, run := range input.Runs {
		if run.Arm != arm {
			providers := make(map[string]bool)
			issues := []string{}
			for index, search := range run.Searches {
				audit, _, _ := parityParseSearch(search, index, provider)
				if audit.ActualProvider != "" {
					providers[audit.ActualProvider] = true
				}
				for _, issue := range audit.Issues {
					issues = append(issues, fmt.Sprintf("search %d: %s", index, issue))
				}
			}
			capture.IgnoredRuns = append(capture.IgnoredRuns, parityIgnoredRun{Scenario: run.Scenario, Arm: run.Arm, ActualProviders: paritySortedSet(providers), PayloadIssues: issues,
				Reason: "Not the selected pure-provider arm. Mixed/fallback arms are excluded wholesale, even if a particular result reports the expected provider."})
			continue
		}
		if strings.TrimSpace(run.Scenario) == "" {
			return parityCapture{}, errors.New("selected parity run has no scenario name")
		}
		if _, duplicate := selected[run.Scenario]; duplicate {
			return parityCapture{}, fmt.Errorf("duplicate selected scenario %q; do not silently merge distinct runs", run.Scenario)
		}
		selected[run.Scenario], names[run.Scenario] = run, true
	}
	sort.Slice(capture.IgnoredRuns, func(i, j int) bool {
		return capture.IgnoredRuns[i].Scenario+"\x00"+capture.IgnoredRuns[i].Arm < capture.IgnoredRuns[j].Scenario+"\x00"+capture.IgnoredRuns[j].Arm
	})
	for _, name := range paritySortedSet(names) {
		run, present := selected[name]
		scenario := parityScenarioCapture{Scenario: name, Missing: !present, Spoken: run.Spoken, RunError: run.Error, ProviderEligible: present,
			ExclusionReasons: []string{}, Searches: []paritySearchAudit{}, Sources: []paritySourceRow{}, URLs: []string{}, Domains: []string{}}
		if !present {
			scenario.ExclusionReasons = append(scenario.ExclusionReasons, "scenario absent from selected arm")
			capture.Scenarios = append(capture.Scenarios, scenario)
			continue
		}
		scenario.SearchCount, scenario.NoSearchesRecorded = len(run.Searches), len(run.Searches) == 0
		distinct := make(map[string]paritySourceRow)
		for index, search := range run.Searches {
			audit, rows, contaminated := parityParseSearch(search, index, provider)
			scenario.Searches = append(scenario.Searches, audit)
			if contaminated {
				scenario.ProviderEligible = false
				scenario.ExclusionReasons = append(scenario.ExclusionReasons, fmt.Sprintf("search %d has a missing/foreign/malformed provider payload", index))
			}
			if audit.Failure != "" {
				scenario.FailedSearchCount++
			}
			if !audit.ProviderVerified {
				scenario.UnverifiedSearchCount++
			}
			if audit.ProviderVerified && audit.Failure == "" && audit.ResultCount == 0 {
				scenario.EmptySearchCount++
			}
			scenario.ResultOccurrences += audit.ResultCount
			scenario.UsableResultOccurrences += len(rows)
			scenario.MalformedRowCount += len(audit.MalformedRows)
			for _, row := range rows {
				keyBytes, _ := json.Marshal(paritySourceFixture([]paritySourceRow{row})[0])
				key := string(keyBytes)
				if prior, exists := distinct[key]; exists {
					prior.Occurrences = append(prior.Occurrences, row.Occurrences...)
					distinct[key] = prior
				} else {
					distinct[key] = row
				}
			}
		}
		if scenario.ProviderEligible {
			keys := make(map[string]bool)
			for key := range distinct {
				keys[key] = true
			}
			urls, domains := make(map[string]bool), make(map[string]bool)
			for _, key := range paritySortedSet(keys) {
				row := distinct[key]
				scenario.Sources = append(scenario.Sources, row)
				canonical, host, _ := parityURLIdentity(row.URL)
				urls[canonical], domains[host] = true, true
				scenario.DistinctSourceTextBytes += paritySourceTextBytes(row.webEvidenceSource)
				if row.Title != "" {
					scenario.Metadata.WithTitle++
				}
				if row.Snippet != "" {
					scenario.Metadata.WithSnippet++
				}
				if len(row.PageExcerpts) > 0 {
					scenario.Metadata.WithPageExcerpts++
				}
				if row.PagePublishedDate != "" {
					scenario.Metadata.WithPagePublicationDate++
				}
				if row.PublishedDate != "" {
					scenario.Metadata.WithPublicationDate++
					_, timestampErr := time.Parse(time.RFC3339, row.PublishedDate)
					_, dateErr := time.Parse("2006-01-02", row.PublishedDate)
					if timestampErr == nil || dateErr == nil {
						scenario.Metadata.ParseableDate++
					}
				}
			}
			scenario.URLs, scenario.Domains = paritySortedSet(urls), paritySortedSet(domains)
		}
		scenario.DistinctSourceRows = len(scenario.Sources)
		fixture, _ := json.Marshal(paritySourceFixture(scenario.Sources))
		scenario.DistinctSourceJSONBytes = len(fixture)
		capture.Scenarios = append(capture.Scenarios, scenario)
	}
	return capture, nil
}

type parityScenarioComparison struct {
	Scenario                   string   `json:"scenario"`
	BaselineMissing            bool     `json:"baseline_missing"`
	CandidateMissing           bool     `json:"candidate_missing"`
	SourceDiagnosticsUsable    bool     `json:"source_diagnostics_usable"`
	SpokenIdentical            bool     `json:"spoken_identical"`
	QuerySequencesIdentical    bool     `json:"query_sequences_identical"`
	ArgumentSequencesIdentical bool     `json:"argument_sequences_identical"`
	BaselineQueries            []string `json:"baseline_queries"`
	CandidateQueries           []string `json:"candidate_queries"`
	SharedURLs                 []string `json:"diagnostic_shared_canonical_urls"`
	BaselineOnlyURLs           []string `json:"diagnostic_baseline_only_urls"`
	CandidateOnlyURLs          []string `json:"diagnostic_candidate_only_urls"`
	SharedDomains              []string `json:"diagnostic_shared_exact_hostnames"`
	BaselineOnlyDomains        []string `json:"diagnostic_baseline_only_hostnames"`
	CandidateOnlyDomains       []string `json:"diagnostic_candidate_only_hostnames"`
	Caveats                    []string `json:"caveats"`
}

type parityComparison struct {
	CandidateReport         string                     `json:"candidate_report"`
	ReportStartDeltaSeconds int64                      `json:"report_start_delta_seconds"`
	ReportStartsIdentical   bool                       `json:"report_starts_identical"`
	Scenarios               []parityScenarioComparison `json:"scenarios"`
}

func paritySetComparison(left, right []string) (shared, leftOnly, rightOnly []string) {
	l, r := make(map[string]bool), make(map[string]bool)
	for _, value := range left {
		l[value] = true
	}
	for _, value := range right {
		r[value] = true
	}
	s, lo, ro := make(map[string]bool), make(map[string]bool), make(map[string]bool)
	for value := range l {
		if r[value] {
			s[value] = true
		} else {
			lo[value] = true
		}
	}
	for value := range r {
		if !l[value] {
			ro[value] = true
		}
	}
	return paritySortedSet(s), paritySortedSet(lo), paritySortedSet(ro)
}

func compareProviderParity(baseline, candidate parityCapture) parityComparison {
	baseTime, _ := time.Parse(time.RFC3339, baseline.StartedAt)
	candidateTime, _ := time.Parse(time.RFC3339, candidate.StartedAt)
	report := parityComparison{CandidateReport: candidate.Path, ReportStartDeltaSeconds: int64(candidateTime.Sub(baseTime).Seconds()), ReportStartsIdentical: baseTime.Equal(candidateTime)}
	names, left, right := make(map[string]bool), make(map[string]parityScenarioCapture), make(map[string]parityScenarioCapture)
	for _, scenario := range baseline.Scenarios {
		names[scenario.Scenario], left[scenario.Scenario] = true, scenario
	}
	for _, scenario := range candidate.Scenarios {
		names[scenario.Scenario], right[scenario.Scenario] = true, scenario
	}
	for _, name := range paritySortedSet(names) {
		l, leftPresent := left[name]
		r, rightPresent := right[name]
		row := parityScenarioComparison{Scenario: name, BaselineMissing: !leftPresent || l.Missing, CandidateMissing: !rightPresent || r.Missing,
			SourceDiagnosticsUsable: leftPresent && rightPresent && l.ProviderEligible && r.ProviderEligible && len(l.Sources) > 0 && len(r.Sources) > 0,
			SpokenIdentical:         leftPresent && rightPresent && l.Spoken == r.Spoken, BaselineQueries: []string{}, CandidateQueries: []string{},
			Caveats: []string{"URL/domain overlap is diagnostic only. Different sources can provide equivalent coverage; identical domains or URLs can provide different facts, freshness, or misleading evidence. No answer-quality/coverage equivalence is inferred; historical model answers are not gold labels."}}
		leftArgs, rightArgs := []string{}, []string{}
		for _, search := range l.Searches {
			row.BaselineQueries = append(row.BaselineQueries, search.Query)
			var args any
			_ = json.Unmarshal(search.Arguments, &args)
			encoded, _ := json.Marshal(args)
			leftArgs = append(leftArgs, string(encoded))
		}
		for _, search := range r.Searches {
			row.CandidateQueries = append(row.CandidateQueries, search.Query)
			var args any
			_ = json.Unmarshal(search.Arguments, &args)
			encoded, _ := json.Marshal(args)
			rightArgs = append(rightArgs, string(encoded))
		}
		lq, _ := json.Marshal(row.BaselineQueries)
		rq, _ := json.Marshal(row.CandidateQueries)
		la, _ := json.Marshal(leftArgs)
		ra, _ := json.Marshal(rightArgs)
		row.QuerySequencesIdentical = !row.BaselineMissing && !row.CandidateMissing && string(lq) == string(rq)
		row.ArgumentSequencesIdentical = !row.BaselineMissing && !row.CandidateMissing && string(la) == string(ra)
		row.SharedURLs, row.BaselineOnlyURLs, row.CandidateOnlyURLs = paritySetComparison(l.URLs, r.URLs)
		row.SharedDomains, row.BaselineOnlyDomains, row.CandidateOnlyDomains = paritySetComparison(l.Domains, r.Domains)
		if !report.ReportStartsIdentical {
			row.Caveats = append(row.Caveats, "Capture report timestamps differ; currentness and remaining local time may have changed. Search-level timestamps were not recorded.")
		}
		if !row.QuerySequencesIdentical || !row.ArgumentSequencesIdentical {
			row.Caveats = append(row.Caveats, "Query/argument sequences differ, including search count or filters. This is not a controlled same-query provider experiment.")
		}
		if row.BaselineMissing || row.CandidateMissing {
			row.Caveats = append(row.Caveats, "A scenario is missing, not a zero-result equivalent.")
		}
		if l.FailedSearchCount+r.FailedSearchCount > 0 {
			row.Caveats = append(row.Caveats, "Recorded searches failed; inspect per-search errors. No source rows are invented from failures.")
		}
		if l.MalformedRowCount+r.MalformedRowCount > 0 {
			row.Caveats = append(row.Caveats, "Malformed result rows were excluded and counted; usable-source counts differ from reported result counts.")
		}
		if !l.ProviderEligible || !r.ProviderEligible {
			row.Caveats = append(row.Caveats, "A selected arm is missing or provider-contaminated; its complete source fixture is excluded from pure-provider diagnostics.")
		}
		report.Scenarios = append(report.Scenarios, row)
	}
	return report
}

// Offline opt-in report generation. This code never constructs a model,
// provider client, HTTP transport, or extraction tool and cannot spend credits.
func TestCapturedProviderParityReport(t *testing.T) {
	if os.Getenv("RUN_PROVIDER_PARITY_REPORT") != "1" {
		t.Skip("set RUN_PROVIDER_PARITY_REPORT=1 and explicit input/output paths for an offline source-only parity report")
	}
	expected := []string{}
	for _, scenario := range liveEyesWebScenarios() {
		expected = append(expected, scenario.name)
	}
	load := func(path, provider, arm string) parityCapture {
		t.Helper()
		absolute, err := filepath.Abs(path)
		if err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(absolute)
		if err != nil {
			t.Fatal(err)
		}
		capture, loadErr := loadProviderParityCapture(file, absolute, provider, arm, expected)
		if closeErr := file.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		return capture
	}
	baseline := load(requiredLiveEnv(t, "PROVIDER_PARITY_BASELINE_REPORT"), "tavily", "tavily")
	var paths []string
	if json.Unmarshal([]byte(requiredLiveEnv(t, "PROVIDER_PARITY_SEARXNG_REPORTS")), &paths) != nil || len(paths) == 0 {
		t.Fatal("PROVIDER_PARITY_SEARXNG_REPORTS must be a nonempty JSON array of report paths")
	}
	report := struct {
		GeneratedAt string             `json:"generated_at"`
		Scope       string             `json:"scope"`
		Baseline    parityCapture      `json:"baseline"`
		Candidates  []parityCapture    `json:"candidates"`
		Comparisons []parityComparison `json:"comparisons"`
	}{GeneratedAt: time.Now().UTC().Format(time.RFC3339), Baseline: baseline,
		Scope: "Offline source-only diagnostics toward equivalent answer quality and useful coverage, not byte equality. No live calls, prior-answer gold labels, semantic equivalence score, or parity pass/fail is produced. Pure selected arms are validated against every actual response provider; mixed/fallback arms are excluded. Result occurrences, distinct source variants, exact hostname/canonical URL overlap, source bytes and metadata are measurements only. Scores/metadata are retained for audit, not compared as calibrated quality. Different timestamps, questions, filters and failure states require separate controlled evaluation and manual claim-level coverage review."}
	seen := make(map[string]bool)
	for _, path := range paths {
		candidate := load(path, "searxng", "searxng_only")
		if seen[candidate.Path] {
			t.Fatal("duplicate candidate report path")
		}
		seen[candidate.Path] = true
		report.Candidates = append(report.Candidates, candidate)
	}
	sort.Slice(report.Candidates, func(i, j int) bool {
		return report.Candidates[i].StartedAt+"\x00"+report.Candidates[i].Path < report.Candidates[j].StartedAt+"\x00"+report.Candidates[j].Path
	})
	for _, candidate := range report.Candidates {
		report.Comparisons = append(report.Comparisons, compareProviderParity(baseline, candidate))
	}
	output, err := os.OpenFile(requiredLiveEnv(t, "PROVIDER_PARITY_REPORT"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		t.Fatal(err)
	}
	t.Logf("offline source-only parity report: %d baseline scenarios, %d candidate reports; no quality-equivalence verdict", len(baseline.Scenarios), len(report.Candidates))
}
