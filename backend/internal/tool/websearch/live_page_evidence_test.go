package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// A direct public-page extraction diagnostic, not a search benchmark. It uses
// the production SSRF-safe fetcher and no API key, model or search provider.
func TestLivePageEvidenceProbe(t *testing.T) {
	if os.Getenv("RUN_LIVE_PAGE_EVIDENCE_PROBE") != "1" {
		t.Skip("opt in with RUN_LIVE_PAGE_EVIDENCE_PROBE=1 and explicit URLs/report")
	}
	urls := strings.Split(os.Getenv("LIVE_PAGE_EVIDENCE_URLS"), ",")
	if len(urls) == 0 || len(urls) > 5 || strings.TrimSpace(urls[0]) == "" {
		t.Fatal("supply 1-5 comma-separated public URLs")
	}
	file, err := os.OpenFile(os.Getenv("LIVE_PAGE_EVIDENCE_REPORT"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	type pageRecord struct {
		URL        string   `json:"url"`
		Error      string   `json:"error,omitempty"`
		ElapsedMS  int64    `json:"elapsed_ms"`
		TextRunes  int      `json:"text_runes"`
		Structured []string `json:"structured_records"`
		Excerpt    string   `json:"query_excerpt"`
		Markup     string   `json:"page_markup,omitempty"`
	}
	report := struct {
		StartedAt string       `json:"started_at"`
		Mode      string       `json:"mode"`
		Query     string       `json:"query"`
		Pages     []pageRecord `json:"pages"`
	}{StartedAt: time.Now().UTC().Format(time.RFC3339), Mode: "direct_public_page_extraction_no_search_or_model", Query: os.Getenv("LIVE_PAGE_EVIDENCE_QUERY")}
	extractor := newPageExtractor()
	blockPaidPageHosts(extractor)
	for _, rawURL := range urls {
		rawURL = strings.TrimSpace(rawURL)
		_, hostname, ok := normalizePublicResultURL(rawURL)
		if !ok || hostname == "tavily.com" || strings.HasSuffix(hostname, ".tavily.com") {
			t.Fatal("invalid public URL or Tavily host in page-only probe")
		}
		ctx, cancel := context.WithTimeout(context.Background(), pageTimeout)
		start := time.Now()
		page, err := extractor.fetchPage(ctx, rawURL)
		cancel()
		record := pageRecord{URL: rawURL, ElapsedMS: time.Since(start).Milliseconds(), TextRunes: utf8.RuneCountInString(page.text), Structured: []string{}}
		if err != nil {
			record.Error = err.Error()
		} else {
			// Explicit diagnostic opt-in only. The production fetcher's existing
			// public-target and 2-MiB response bounds remain in effect.
			if os.Getenv("LIVE_PAGE_EVIDENCE_INCLUDE_MARKUP") == "1" {
				record.Markup = page.markup
			}
			for _, line := range strings.Split(page.text, "\n") {
				if strings.HasPrefix(line, "Page structured data: ") {
					record.Structured = append(record.Structured, line)
				}
			}
			record.Excerpt = selectQueryChunks(report.Query, page.text)
		}
		report.Pages = append(report.Pages, record)
		t.Logf("page=%s structured=%d elapsed_ms=%d error=%s", rawURL, len(record.Structured), record.ElapsedMS, record.Error)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		t.Fatal(err)
	}
}

func blockPaidPageHosts(extractor *pageExtractor) {
	validate := extractor.validateURL
	extractor.validateURL = func(ctx context.Context, target *url.URL) error {
		host := strings.ToLower(strings.TrimSuffix(target.Hostname(), "."))
		if host == "tavily.com" || strings.HasSuffix(host, ".tavily.com") {
			return errors.New("paid provider hosts are blocked in page probes")
		}
		return validate(ctx, target)
	}
}

// Explicit landing-page inputs isolate linked acquisition from search discovery
// and synthesis. A successful harness does not imply satisfactory answers.
func TestLiveLinkedPageEvidenceProbe(t *testing.T) {
	if os.Getenv("RUN_LIVE_LINKED_PAGE_EVIDENCE_PROBE") != "1" {
		t.Skip("opt in with RUN_LIVE_LINKED_PAGE_EVIDENCE_PROBE=1 and explicit URLs/report")
	}
	urls := strings.Split(os.Getenv("LIVE_PAGE_EVIDENCE_URLS"), ",")
	if len(urls) == 0 || len(urls) > 5 || strings.TrimSpace(urls[0]) == "" {
		t.Fatal("supply 1-5 comma-separated public landing page URLs")
	}
	var seeds []Result
	for _, rawURL := range urls {
		normalized, _, ok := normalizePublicResultURL(rawURL)
		if !ok {
			t.Fatal("invalid page probe URL")
		}
		seeds = append(seeds, Result{URL: normalized})
	}
	file, err := os.OpenFile(os.Getenv("LIVE_PAGE_EVIDENCE_REPORT"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	extractor := newPageExtractor()
	blockPaidPageHosts(extractor)
	start := time.Now()
	query := os.Getenv("LIVE_PAGE_EVIDENCE_QUERY")
	results, extracted := extractor.enrich(context.Background(), query, seeds)
	report := struct {
		StartedAt        string   `json:"started_at"`
		Mode             string   `json:"mode"`
		Query            string   `json:"query"`
		ElapsedMS        int64    `json:"elapsed_ms"`
		Inputs           []Result `json:"explicit_landing_page_inputs"`
		Results          []Result `json:"results"`
		Extracted        int      `json:"extracted_results"`
		MaxChildAttempts int      `json:"max_child_attempts"`
	}{start.UTC().Format(time.RFC3339), "linked_page_acquisition_no_search_or_model", query, time.Since(start).Milliseconds(), seeds, results, extracted, maxLinkedPageFetches}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		t.Fatal(err)
	}
	t.Logf("inputs=%d appended_sources=%d extracted=%d elapsed_ms=%d", len(seeds), len(results)-len(seeds), extracted, report.ElapsedMS)
}
