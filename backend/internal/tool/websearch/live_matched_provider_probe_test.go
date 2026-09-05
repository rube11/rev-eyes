package websearch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/tool"
	"golang.org/x/net/idna"
)

const matchedProbeSourceSHA = "695112fdfa056e8e127fa46263c5f47564ccfeac95e4998b2e95ab4b4aed7cdb"

type matchedProbeCase struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	AsOf     string `json:"as_of"`
	TimeZone string `json:"time_zone"`
}

type matchedProbeInput struct {
	Case      matchedProbeCase `json:"case"`
	Arguments json.RawMessage  `json:"arguments"`
}

func matchedProbeInputs(data []byte) ([]matchedProbeInput, error) {
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != matchedProbeSourceSHA {
		return nil, errors.New("frozen source hash mismatch")
	}
	var saved struct {
		Runs []struct {
			Case     matchedProbeCase `json:"case"`
			Searches []struct {
				Arguments json.RawMessage `json:"arguments"`
			} `json:"searches"`
		} `json:"runs"`
	}
	if json.Unmarshal(data, &saved) != nil || len(saved.Runs) != 8 {
		return nil, errors.New("expected eight frozen cases")
	}
	seen := map[string]bool{}
	var inputs []matchedProbeInput
	for _, run := range saved.Runs {
		if run.Case.ID == "" || seen[run.Case.ID] || run.Case.Question == "" || len(run.Searches) == 0 {
			return nil, errors.New("missing or duplicate frozen case")
		}
		seen[run.Case.ID] = true
		args := run.Searches[0].Arguments
		parsed, err := decodeSearchInput(args)
		if err != nil || parsed.Mode != "research" {
			return nil, errors.New("invalid frozen research arguments")
		}
		if _, err := time.Parse(time.RFC3339, run.Case.AsOf); err != nil {
			return nil, err
		}
		inputs = append(inputs, matchedProbeInput{run.Case, append(json.RawMessage(nil), args...)})
	}
	return inputs, nil
}

type matchedProbeHTTP struct {
	Request    json.RawMessage `json:"public_request"`
	Status     int             `json:"http_status"`
	Response   json.RawMessage `json:"raw_response,omitempty"`
	Credits    *int            `json:"reported_credits"`
	Incomplete bool            `json:"incomplete"`
	Redacted   bool            `json:"secret_redacted"`
}

type matchedPaidTransport struct {
	next     http.RoundTripper
	expected searchRequest
	key      string
	requests int
	stopped  bool
	records  []matchedProbeHTTP
}

func matchedExpectedRequest(input searchInput) searchRequest {
	// The wire format omits empty domain filters. Canonicalize only this
	// representation difference; preserve nonempty filters and their order.
	domains := append([]string(nil), input.IncludeDomains...)
	return searchRequest{Query: input.Query, SearchDepth: "advanced", ChunksPerSource: 3,
		MaxResults: 8, Topic: input.Topic, TimeRange: searchTimeRange(input.Recency), IncludeDomains: domains,
		Language: "en", FilterLanguage: true, SafeSearch: true, IncludeUsage: true}
}

func (p *matchedPaidTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if p.stopped || p.requests >= 8 {
		return nil, errors.New("isolated Tavily budget stopped")
	}
	if req.Method != http.MethodPost || req.URL.String() != searchURL || (req.Host != "" && req.Host != "api.tavily.com") {
		return nil, errors.New("isolated Tavily endpoint mismatch")
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, 8193))
	if err != nil || len(body) > 8192 {
		return nil, errors.New("invalid bounded public request")
	}
	var sent searchRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&sent) != nil || decoder.Decode(&struct{}{}) != io.EOF || !reflect.DeepEqual(sent, p.expected) {
		return nil, errors.New("request differs from matched query or advanced-search cap")
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	p.requests++
	record := matchedProbeHTTP{Request: append(json.RawMessage(nil), body...)}
	resp, err := p.next.RoundTrip(req)
	if err != nil {
		record.Incomplete = true
		p.stopped = true
		p.records = append(p.records, record)
		return nil, errors.New("Tavily transport incomplete; no retry permitted")
	}
	record.Status = resp.StatusCode
	response, large, readErr := readBounded(resp.Body, maxResponseBodySize)
	resp.Body.Close()
	if readErr != nil || large || !json.Valid(response) {
		record.Incomplete = true
		p.stopped = true
		p.records = append(p.records, record)
		return nil, errors.New("Tavily response incomplete; no retry permitted")
	}
	// Do not persist headers or credentials, even if an unexpected server echo
	// contains the token. Redaction is explicit, never labeled byte-identical raw.
	if p.key != "" && bytes.Contains(response, []byte(p.key)) {
		response = bytes.ReplaceAll(response, []byte(p.key), []byte("[REDACTED]"))
		record.Redacted = true
	}
	record.Response = append(json.RawMessage(nil), response...)
	var usage struct {
		Usage struct {
			Credits *int `json:"credits"`
		} `json:"usage"`
	}
	if json.Unmarshal(response, &usage) == nil {
		record.Credits = usage.Usage.Credits
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || record.Credits == nil || *record.Credits < 0 || *record.Credits > 2 {
		p.stopped = true
	}
	p.records = append(p.records, record)
	resp.Body = io.NopCloser(bytes.NewReader(response))
	return resp, nil
}

type matchedDenyPaid struct {
	next     http.RoundTripper
	attempts *atomic.Int64
}

func matchedPaidHost(host string) bool {
	// Match Go's internationalized-host lookup normalization before comparing.
	if ascii, err := idna.Lookup.ToASCII(host); err == nil {
		host = ascii
	} else {
		return true
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "tavily.com" || strings.HasSuffix(host, ".tavily.com") || host == "openai.com" || strings.HasSuffix(host, ".openai.com")
}
func (p matchedDenyPaid) RoundTrip(req *http.Request) (*http.Response, error) {
	hostOverride := (&url.URL{Host: req.Host}).Hostname()
	if matchedPaidHost(req.URL.Hostname()) || (hostOverride != "" && matchedPaidHost(hostOverride)) {
		p.attempts.Add(1)
		return nil, errors.New("paid/model host blocked outside isolated Tavily client")
	}
	return p.next.RoundTrip(req)
}

// Explicit exception for the user-approved baseline only. Existing no-Tavily
// evaluators and production defaults are unchanged. No answer model is invoked.
func TestLiveMatchedProviderProbe(t *testing.T) {
	if os.Getenv("RUN_LIVE_MATCHED_PROVIDER_PROBE") != "1" {
		t.Skip("explicit matched retrieval probe opt-in required")
	}
	file, err := os.Open(os.Getenv("LIVE_MATCHED_PROVIDER_PROBE_SOURCE"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(io.LimitReader(file, (16<<20)+1))
	file.Close()
	if err != nil || len(data) > 16<<20 {
		t.Fatal("source exceeds bound")
	}
	inputs, err := matchedProbeInputs(data)
	if err != nil {
		t.Fatal(err)
	}
	base := os.Getenv("SEARXNG_BASE_URL")
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" || u.Host != "127.0.0.1:8889" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		t.Fatal("only the inspected loopback evaluation server is allowed")
	}
	paid, err := New(os.Getenv("TAVILY_API_KEY"))
	if err != nil {
		t.Fatal("approved Tavily key must be provided privately")
	}
	free, err := newSearXNG(base, true)
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.OpenFile(os.Getenv("LIVE_MATCHED_PROVIDER_PROBE_REPORT"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	type armResult struct {
		Provider     string            `json:"provider"`
		Arguments    json.RawMessage   `json:"arguments"`
		ArgumentsSHA string            `json:"arguments_sha256"`
		StartedAt    string            `json:"started_at"`
		ElapsedMS    int64             `json:"elapsed_ms"`
		Content      json.RawMessage   `json:"normalized_response,omitempty"`
		Error        string            `json:"error,omitempty"`
		NotRun       bool              `json:"not_run"`
		HTTP         *matchedProbeHTTP `json:"tavily_http,omitempty"`
	}
	type pairedCase struct {
		Input matchedProbeInput `json:"input"`
		Arms  []armResult       `json:"arms"`
	}
	report := struct {
		Mode            string       `json:"mode"`
		Scope           string       `json:"scope"`
		SourceSHA       string       `json:"source_sha256"`
		StartedAt       string       `json:"started_at"`
		FinishedAt      string       `json:"finished_at"`
		SettingsSHA     string       `json:"root_verified_settings_sha256"`
		Revision        string       `json:"searxng_revision"`
		PaidRequests    int          `json:"tavily_attempts"`
		ReportedCredits int          `json:"known_reported_credits"`
		UnknownUsage    int          `json:"requests_with_unknown_usage"`
		BlockedAttempts int64        `json:"forbidden_free_or_model_host_attempts"`
		Cases           []pairedCase `json:"cases"`
	}{Mode: "matched_native_retrieval_no_answer_model", SourceSHA: matchedProbeSourceSHA, StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
		SettingsSHA: "34e0a35f0c3b86428959e0f5c99e3f0a986d29caf80f6e791eea322980634f95", Revision: "a1144dda3e97668c9d445022b7019c224cd4bb1e",
		Scope: "Exactly the eight frozen live10 questions/clocks and their first recorded search arguments. Each native provider receives byte-identical arguments once; paired order alternates. Native content is not equivalent provenance: Tavily search content versus SearXNG discovery and separately fetched blocks. Both normalized source budgets are 1600 runes; SearXNG may add two linked rows. Raw Tavily response retained separately. No answer-model, router, memory, query rewriting, retry, new provider plan, or production fallback change. Current retrieval occurs after the supplied historical case cutoff; time_range is provider-relative to execution, so later evidence must be excluded manually. Joint retrieval failure cannot prove absence on the internet. Root checked only Yahoo and Bing News in /config before execution; recorded configuration hashes are root-observed metadata, not independent per-request engine-health proof."}
	standard := http.DefaultTransport
	transport, ok := standard.(*http.Transport)
	if !ok {
		output.Close()
		t.Fatal("expected standard transport before installing tripwire")
	}
	paidBase := transport.Clone()
	paidBase.Proxy = nil
	p := &matchedPaidTransport{next: paidBase, key: paid.apiKey}
	paid.client = &http.Client{Timeout: 20 * time.Second, Transport: p, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Tavily redirects disabled") }}
	var blocked atomic.Int64
	http.DefaultTransport = matchedDenyPaid{standard, &blocked}
	free.client.Transport = matchedDenyPaid{standard, &blocked}
	validate := free.extractor.validateURL
	free.extractor.validateURL = func(ctx context.Context, target *url.URL) error {
		if matchedPaidHost(target.Hostname()) {
			blocked.Add(1)
			return errors.New("paid/model page host blocked in free arm")
		}
		return validate(ctx, target)
	}
	t.Cleanup(func() {
		http.DefaultTransport = standard
		paidBase.CloseIdleConnections()
		report.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		report.PaidRequests = p.requests
		report.BlockedAttempts = blocked.Load()
		for _, r := range p.records {
			if r.Credits == nil {
				report.UnknownUsage++
			} else {
				report.ReportedCredits += *r.Credits
			}
		}
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			t.Errorf("write report: %v", err)
		}
		output.Close()
	})
	for index, input := range inputs {
		pair := pairedCase{Input: input}
		order := []string{ProviderTavily, ProviderSearXNG}
		if index%2 == 1 {
			order[0], order[1] = order[1], order[0]
		}
		for _, provider := range order {
			digest := sha256.Sum256(input.Arguments)
			arm := armResult{Provider: provider, Arguments: append(json.RawMessage(nil), input.Arguments...), ArgumentsSHA: hex.EncodeToString(digest[:]), StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
			if provider == ProviderTavily && p.stopped {
				arm.NotRun = true
				arm.Error = "paid safety stop; no retry"
				pair.Arms = append(pair.Arms, arm)
				continue
			}
			start := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			var result tool.Result
			var callErr error
			if provider == ProviderTavily {
				parsed, _ := decodeSearchInput(input.Arguments)
				p.expected = matchedExpectedRequest(parsed)
				before := len(p.records)
				result, callErr = paid.Execute(ctx, tool.Scope{}, append(json.RawMessage(nil), input.Arguments...))
				if len(p.records) > before {
					record := p.records[len(p.records)-1]
					arm.HTTP = &record
				}
			} else {
				result, callErr = free.Execute(ctx, tool.Scope{}, append(json.RawMessage(nil), input.Arguments...))
			}
			cancel()
			arm.ElapsedMS = time.Since(start).Milliseconds()
			if callErr != nil {
				arm.Error = callErr.Error()
			} else if json.Valid([]byte(result.Content)) {
				arm.Content = json.RawMessage(result.Content)
			} else {
				arm.Error = "non-JSON normalized result"
			}
			pair.Arms = append(pair.Arms, arm)
			t.Logf("case=%s provider=%s elapsed_ms=%d error=%s", input.Case.ID, provider, arm.ElapsedMS, arm.Error)
		}
		if !bytes.Equal(pair.Arms[0].Arguments, pair.Arms[1].Arguments) {
			t.Fatal("paired input bytes changed")
		}
		report.Cases = append(report.Cases, pair)
	}
	if p.requests > 8 || blocked.Load() != 0 {
		t.Fatal("comparison guard invariant failed")
	}
}

type matchedRoundTripFunc func(*http.Request) (*http.Response, error)

func (f matchedRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMatchedProviderProbeFrozenInputs(t *testing.T) {
	data := readWebResearchTestCorpus(t, "heldout-pilot-live10-excerpt-refs-2026-09-04.json")
	inputs, err := matchedProbeInputs(data)
	if err != nil || len(inputs) != 8 {
		t.Fatalf("frozen input load: %v", err)
	}
	if _, err := matchedProbeInputs(append(data, ' ')); err == nil {
		t.Fatal("changed fixture accepted")
	}
}

func TestMatchedProviderProbeBudgetAndQueryBinding(t *testing.T) {
	input := searchInput{Query: "fixed query", Mode: "research", Topic: "general", Recency: "none"}
	expected := matchedExpectedRequest(input)
	calls := 0
	p := &matchedPaidTransport{expected: expected, next: matchedRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"results":[],"usage":{"credits":2}}`)), Header: make(http.Header)}, nil
	})}
	body, _ := json.Marshal(expected)
	for i := 0; i < 9; i++ {
		req, _ := http.NewRequest(http.MethodPost, searchURL, bytes.NewReader(body))
		_, err := p.RoundTrip(req)
		if (i < 8) != (err == nil) {
			t.Fatalf("request %d guard outcome: %v", i, err)
		}
	}
	if calls != 8 || p.requests != 8 {
		t.Fatal("paid cap not enforced")
	}
	p = &matchedPaidTransport{expected: expected, next: matchedRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("mismatched request reached network")
		return nil, nil
	})}
	changed := expected
	changed.Query = "different question"
	body, _ = json.Marshal(changed)
	req, _ := http.NewRequest(http.MethodPost, searchURL, bytes.NewReader(body))
	if _, err := p.RoundTrip(req); err == nil {
		t.Fatal("query mismatch accepted")
	}
}

func TestMatchedProviderProbeStopsOnUnknownUsageAndBlocksOtherClients(t *testing.T) {
	input := searchInput{Query: "fixed query", Mode: "research", Topic: "general", Recency: "none"}
	expected := matchedExpectedRequest(input)
	body, _ := json.Marshal(expected)
	for _, response := range []string{`{"results":[]}`, `{"usage":{"credits":3}}`, `{"usage":{"credits":3.5}}`, `{"usage":{"credits":"3"}}`} {
		calls := 0
		p := &matchedPaidTransport{expected: expected, next: matchedRoundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(response))}, nil
		})}
		req, _ := http.NewRequest(http.MethodPost, searchURL, bytes.NewReader(body))
		p.RoundTrip(req)
		req, _ = http.NewRequest(http.MethodPost, searchURL, bytes.NewReader(body))
		if _, err := p.RoundTrip(req); err == nil || calls != 1 {
			t.Fatal("unknown/over-budget usage did not stop")
		}
	}
	var blocked atomic.Int64
	deny := matchedDenyPaid{matchedRoundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("blocked host reached network"); return nil, nil }), &blocked}
	for _, endpoint := range []string{searchURL, "https://api.openai.com/v1/responses", "https://api.tavily.com./search", "https://api\uff0etavily\uff0ecom/search", "https://api.ta\u00advily.com/search"} {
		req, _ := http.NewRequest(http.MethodGet, endpoint, nil)
		if _, err := deny.RoundTrip(req); err == nil {
			t.Fatal("paid/model host escaped guard")
		}
	}
	if blocked.Load() != 5 {
		t.Fatal("blocked attempt accounting wrong")
	}
}

func TestMatchedProviderProbeErrorStopAndRedaction(t *testing.T) {
	expected := matchedExpectedRequest(searchInput{Query: "fixed query", Topic: "general", Recency: "none", IncludeDomains: []string{"nasa.gov"}})
	body, _ := json.Marshal(expected)
	for _, status := range []int{200, 429} {
		p := &matchedPaidTransport{expected: expected, key: "fake-test-token", next: matchedRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"echo":"fake-test-token","usage":{"credits":2}}`))}, nil
		})}
		req, _ := http.NewRequest(http.MethodPost, searchURL, bytes.NewReader(body))
		resp, err := p.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if !p.records[0].Redacted || bytes.Contains(p.records[0].Response, []byte(p.key)) || p.stopped != (status != 200) {
			t.Fatal("redaction or HTTP error stop failed")
		}
	}
	p := &matchedPaidTransport{expected: expected, next: matchedRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("fake transport failure")
	})}
	req, _ := http.NewRequest(http.MethodPost, searchURL, bytes.NewReader(body))
	if _, err := p.RoundTrip(req); err == nil || !p.stopped || p.requests != 1 || !p.records[0].Incomplete {
		t.Fatal("transport failure did not stop the run")
	}
	for _, input := range []searchInput{
		{Query: "fixed query", Topic: "general", Recency: "none", IncludeDomains: []string{"example.org"}},
		{Query: "fixed query", Topic: "news", Recency: "none", IncludeDomains: []string{"nasa.gov"}},
		{Query: "fixed query", Topic: "general", Recency: "week", IncludeDomains: []string{"nasa.gov"}},
	} {
		p := &matchedPaidTransport{expected: expected, next: matchedRoundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("changed filters reached network")
			return nil, nil
		})}
		changed, _ := json.Marshal(matchedExpectedRequest(input))
		req, _ := http.NewRequest(http.MethodPost, searchURL, bytes.NewReader(changed))
		if _, err := p.RoundTrip(req); err == nil || p.requests != 0 {
			t.Fatal("filter mismatch accepted")
		}
	}
}
