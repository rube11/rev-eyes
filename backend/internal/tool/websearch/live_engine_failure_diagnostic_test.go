package websearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

type engineFailureProbe struct {
	Engine              string            `json:"engine"`
	CaseID              string            `json:"case_id"`
	Query               string            `json:"query"`
	Form                url.Values        `json:"request_form"`
	ElapsedMS           int64             `json:"elapsed_ms"`
	HTTPStatus          int               `json:"http_status,omitempty"`
	ResultCount         int               `json:"raw_result_count"`
	UnresponsiveEngines []json.RawMessage `json:"unresponsive_engines"`
	Results             []searxResult     `json:"first_results,omitempty"`
	Error               string            `json:"error,omitempty"`
	Skipped             string            `json:"skipped,omitempty"`
}

// A retrieval-only diagnostic, not an answer-quality run. It never invokes the
// model, a page fetcher, Tavily, or a new search provider. Each current general
// engine gets at most two requests and is stopped on its first reported error.
func TestLiveEngineFailureDiagnostic(t *testing.T) {
	if os.Getenv("RUN_LIVE_ENGINE_FAILURE_DIAGNOSTIC") != "1" {
		t.Skip("opt in with RUN_LIVE_ENGINE_FAILURE_DIAGNOSTIC=1 and an exclusive report path")
	}
	baseURL := os.Getenv("ENGINE_FAILURE_DIAGNOSTIC_BASE_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8889"
	}
	if !engineDiagnosticLoopbackURL(baseURL) {
		t.Fatal("diagnostic requires a loopback HTTP base URL without credentials, query, fragment, or path")
	}
	raw, err := os.ReadFile("../../../docs/heldout-pilot-live8-source-review-2026-09-04.json")
	if err != nil {
		t.Fatal(err)
	}
	var capture struct {
		Runs []struct {
			Case struct {
				ID string `json:"id"`
			} `json:"case"`
			Searches []struct {
				Arguments searchInput `json:"arguments"`
			} `json:"searches"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(raw, &capture); err != nil {
		t.Fatal(err)
	}
	var cases []engineFailureProbe
	for _, caseID := range []string{"henderson_saturday_outdoor_concert", "valley_of_fire_day_use"} {
		for _, run := range capture.Runs {
			if run.Case.ID != caseID || len(run.Searches) == 0 {
				continue
			}
			input := run.Searches[0].Arguments
			if input.Topic != "general" || input.Recency != "none" || len(input.IncludeDomains) != 0 {
				t.Fatal("saved first query no longer matches this unfiltered general diagnostic")
			}
			cases = append(cases, engineFailureProbe{CaseID: caseID, Query: input.Query})
		}
	}
	if len(cases) != 2 {
		t.Fatal("expected both unchanged first-query fixtures")
	}
	output, err := os.OpenFile(os.Getenv("ENGINE_FAILURE_DIAGNOSTIC_REPORT"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	report := struct {
		StartedAt           string               `json:"started_at"`
		Mode                string               `json:"mode"`
		BaseURL             string               `json:"base_url"`
		SourceCaptureSHA256 string               `json:"source_capture_sha256"`
		Scope               string               `json:"scope"`
		Probes              []engineFailureProbe `json:"probes"`
	}{time.Now().UTC().Format(time.RFC3339), "current_isolated_engine_diagnostic_no_model_or_page_fetch", baseURL,
		hex.EncodeToString(digest[:]), "Current Google/Yahoo observations only, not proof of historical live8 causality. No categories with engine isolation, no engine-setting changes, no retries after reported failures, no paid providers or credential access.", nil}
	defer func() {
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			t.Error(err)
		}
		if err := output.Close(); err != nil {
			t.Error(err)
		}
	}()
	client := &http.Client{Timeout: searxSearchTimeout, Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	for _, engine := range []string{"google", "yahoo"} {
		stopped := false
		for _, item := range cases {
			item.Engine = engine
			if stopped {
				item.Skipped = "Engine reported an earlier error; no follow-up request sent."
			} else {
				item = runEngineFailureProbe(client, baseURL, item)
				stopped = item.Error != "" || len(item.UnresponsiveEngines) > 0
			}
			report.Probes = append(report.Probes, item)
			t.Logf("engine=%s case=%s results=%d unresponsive=%d error=%q skipped=%t elapsed_ms=%d", engine, item.CaseID, item.ResultCount, len(item.UnresponsiveEngines), item.Error, item.Skipped != "", item.ElapsedMS)
		}
	}
}

func engineDiagnosticLoopbackURL(raw string) bool {
	target, err := url.Parse(raw)
	if err != nil || target.Scheme != "http" || target.User != nil || target.ForceQuery || target.RawQuery != "" || target.Fragment != "" || (target.Path != "" && target.Path != "/") {
		return false
	}
	address := net.ParseIP(target.Hostname())
	return address != nil && address.IsLoopback()
}

func runEngineFailureProbe(client *http.Client, baseURL string, probe engineFailureProbe) engineFailureProbe {
	started := time.Now()
	probe.Form = url.Values{"q": {probe.Query}, "engines": {probe.Engine}, "format": {"json"}, "language": {"en"}, "safesearch": {"2"}}
	// Explicit engines and categories must not be combined: SearXNG unions them.
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, strings.TrimRight(baseURL, "/")+"/search", strings.NewReader(probe.Form.Encode()))
	if err != nil {
		probe.Error = "create diagnostic request failed"
		return probe
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		probe.Error = "send diagnostic request failed"
		probe.ElapsedMS = time.Since(started).Milliseconds()
		return probe
	}
	defer response.Body.Close()
	probe.HTTPStatus = response.StatusCode
	body, oversized, err := readBounded(response.Body, maxResponseBodySize)
	probe.ElapsedMS = time.Since(started).Milliseconds()
	if err != nil || oversized {
		probe.Error = "read bounded diagnostic response failed"
		return probe
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		probe.Error = fmt.Sprintf("diagnostic HTTP status %d", response.StatusCode)
		return probe
	}
	var payload searxResponse
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	if err := decoder.Decode(&payload); err != nil {
		probe.Error = "decode diagnostic response failed"
		return probe
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		probe.Error = "diagnostic response has trailing data"
		return probe
	}
	probe.ResultCount = len(payload.Results)
	probe.UnresponsiveEngines = payload.UnresponsiveEngines // Preserve reason and suspension data verbatim, not just counts.
	probe.Results = payload.Results[:min(8, len(payload.Results))]
	for _, result := range payload.Results {
		for _, engine := range result.Engines {
			if engine != probe.Engine {
				probe.Error = "engine isolation failed: unexpected contributing engine"
			}
		}
	}
	return probe
}

func TestEngineDiagnosticRequiresLiteralLoopbackEndpoint(t *testing.T) {
	for _, target := range []string{"https://127.0.0.1:8889", "http://example.com", "http://127.0.0.1.evil.test", "http://user@127.0.0.1:8889", "http://127.0.0.1/search", "http://127.0.0.1?q=x", "http://127.0.0.1?", "http://127.0.0.1#x"} {
		if engineDiagnosticLoopbackURL(target) {
			t.Errorf("unsafe diagnostic endpoint accepted: %s", target)
		}
	}
	for _, target := range []string{"http://127.0.0.1:8889", "http://[::1]:8889/"} {
		if !engineDiagnosticLoopbackURL(target) {
			t.Errorf("loopback endpoint rejected: %s", target)
		}
	}
}

func TestEngineDiagnosticPreservesRawSuspensionAndSendsOnlyIsolatedEngine(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests++
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if request.Form.Get("engines") != "google" || request.Form.Has("categories") || request.Form.Get("q") != "unchanged public query" {
			t.Errorf("diagnostic changed query or mixed engine/category selection: %v", request.Form)
		}
		_, _ = io.WriteString(w, `{"results":[],"unresponsive_engines":[["google","CAPTCHA",true]]}`)
	}))
	defer server.Close()
	probe := runEngineFailureProbe(server.Client(), server.URL, engineFailureProbe{Engine: "google", Query: "unchanged public query"})
	if probe.Error != "" || requests != 1 || probe.ResultCount != 0 || len(probe.UnresponsiveEngines) != 1 || string(probe.UnresponsiveEngines[0]) != `["google","CAPTCHA",true]` {
		t.Fatalf("diagnostic lost raw reason/suspension or retried a failed engine: requests=%d probe=%+v", requests, probe)
	}
}
