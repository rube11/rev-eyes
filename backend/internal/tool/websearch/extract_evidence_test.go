package websearch

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestExtractPublicationDateExplicitArticleMetadata(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name   string
		markup string
		want   string
	}{
		{"open_graph", `<meta property="article:published_time" content="2026-09-03T11:30:00Z">`, "2026-09-03T11:30:00Z"},
		{"attribute_order", `<meta content='2026-09-03T04:30:00-07:00' data-extra='ignored' property='article:published_time'>`, "2026-09-03T11:30:00Z"},
		{"case_insensitive_html", `<META CONTENT="2026-09-03" ITEMPROP="datePublished">`, "2026-09-03"},
		{"escaped_attribute_value", `<meta property="article:published_time" content="2026-09-03T11&#58;30&#58;00&#90;">`, "2026-09-03T11:30:00Z"},
		{"itemprop", `<meta itemprop="datePublished" content="2026-09-03">`, "2026-09-03"},
		{"json_ld_article", `<script type="application/ld+json">{"@context":"https://schema.org","@type":"Article","datePublished":"2026-09-03T11:30:00Z"}</script>`, "2026-09-03T11:30:00Z"},
		{"json_ld_news_article", `<script type="application/ld+json">{"@context":"https://schema.org","@type":"NewsArticle","datePublished":"2026-09-03"}</script>`, "2026-09-03"},
		{"json_ld_graph", `<script type="application/ld+json">{"@context":"https://schema.org","@graph":[{"@type":"Organization","datePublished":"2020-01-01"},{"@type":"NewsArticle","datePublished":"2026-09-03"}]}</script>`, "2026-09-03"},
		{"json_ld_array", `<script type="application/ld+json">[{"@type":"BreadcrumbList"},{"@type":"Article","datePublished":"2026-09-03"}]</script>`, "2026-09-03"},
		{"duplicate_graph_dates", `<script type="application/ld+json">{"@graph":[{"@type":"Article","datePublished":"2026-09-03"},{"@type":"NewsArticle","datePublished":"2026-09-03"}]}</script>`, "2026-09-03"},
		{"equivalent_graph_timestamps", `<script type="application/ld+json">{"@graph":[{"@type":"Article","datePublished":"2026-09-03T04:30:00-07:00"},{"@type":"NewsArticle","datePublished":"2026-09-03T11:30:00Z"}]}</script>`, "2026-09-03T11:30:00Z"},
		{"invalid_then_valid", `<meta property="article:published_time" content="2026-02-30"><meta itemprop="datePublished" content="2026-09-03">`, "2026-09-03"},
		{"future_is_not_a_parse_error", `<meta itemprop="datePublished" content="2099-09-03">`, "2099-09-03"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			got := extractPublicationDate(scenario.markup)
			if got == scenario.want {
				return
			}
			// RFC3339 offset preservation and UTC normalization are both valid.
			gotTime, gotErr := time.Parse(time.RFC3339, got)
			wantTime, wantErr := time.Parse(time.RFC3339, scenario.want)
			if gotErr != nil || wantErr != nil || !gotTime.Equal(wantTime) {
				t.Errorf("extractPublicationDate() = %q, want %q or equivalent normalized timestamp", got, scenario.want)
			}
		})
	}
}

func TestExtractPublicationDateRejectsUnrelatedOrInvalidDates(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name   string
		markup string
	}{
		{"date_modified_meta", `<meta property="article:modified_time" content="2026-09-03T11:30:00Z"><meta itemprop="dateModified" content="2026-09-03">`},
		{"date_modified_article", `<script type="application/ld+json">{"@type":"NewsArticle","dateModified":"2026-09-03"}</script>`},
		{"copyright", `<footer>Copyright 2026-09-03 Aurora Observatory. All rights reserved.</footer>`},
		{"navigation", `<nav><a href="/archive/2026-09-03">2026-09-03 archive</a></nav>`},
		{"visible_time_without_metadata", `<article><time datetime="2026-09-03">Sep 3, 2026</time><p>Aurora telescope observations.</p></article>`},
		{"event_json_ld", `<script type="application/ld+json">{"@type":"Event","datePublished":"2026-09-03","startDate":"2026-09-03T20:00:00Z"}</script>`},
		{"organization_json_ld", `<script type="application/ld+json">{"@type":"Organization","datePublished":"2026-09-03"}</script>`},
		{"untyped_json_ld", `<script type="application/ld+json">{"datePublished":"2026-09-03"}</script>`},
		{"publisher_nested_in_article", `<script type="application/ld+json">{"@type":"Article","publisher":{"@type":"Organization","datePublished":"2026-09-03"}}</script>`},
		{"related_article_is_not_current_article", `<script type="application/ld+json">{"@type":"Article","isRelatedTo":{"@type":"NewsArticle","datePublished":"2026-09-03"}}</script>`},
		{"nested_event_is_not_current_article", `<script type="application/ld+json">{"@type":"Article","about":{"@type":"Event","datePublished":"2026-09-03"}}</script>`},
		{"ordinary_script", `<script>{"@type":"Article","datePublished":"2026-09-03"}</script>`},
		{"malformed_json_ld", `<script type="application/ld+json">{"@type":"Article","datePublished":"2026-09-03"</script>`},
		{"invalid_calendar_date", `<meta property="article:published_time" content="2026-02-30">`},
		{"invalid_timestamp", `<meta itemprop="datePublished" content="2026-09-03T28:61:00Z">`},
		{"ambiguous_numeric_date", `<meta itemprop="datePublished" content="03/09/26">`},
		{"date_in_attribute_name", `<meta data-content="2026-09-03" property="article:published_time">`},
		{"commented_out_meta", `<!-- <meta property="article:published_time" content="2026-09-03"> -->`},
		{"meta_literal_in_script", `<script>const example = '<meta property="article:published_time" content="2026-09-03">';</script>`},
		{"commented_out_json_ld", `<!-- <script type="application/ld+json">{"@type":"Article","datePublished":"2026-09-03"}</script> -->`},
		{"ambiguous_graph_articles", `<script type="application/ld+json">{"@graph":[{"@type":"Article","url":"https://aurora.example/old","datePublished":"2020-01-01"},{"@type":"NewsArticle","url":"https://aurora.example/current","datePublished":"2026-09-03"}]}</script>`},
		{"ambiguous_separate_json_ld_blocks", `<script type="application/ld+json">{"@type":"Article","datePublished":"2020-01-01"}</script><script type="application/ld+json">{"@type":"NewsArticle","datePublished":"2026-09-03"}</script>`},
		{"ambiguous_nested_graph_cannot_be_overridden", `<script type="application/ld+json">{"@graph":[{"@graph":[{"@type":"Article","datePublished":"2020-01-01"},{"@type":"Article","datePublished":"2021-01-01"}]},{"@type":"NewsArticle","datePublished":"2026-09-03"}]}</script>`},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if got := extractPublicationDate(scenario.markup); got != "" {
				t.Errorf("extractPublicationDate() = %q; must not infer publication from unrelated or invalid metadata", got)
			}
		})
	}
}

func TestPageEnrichPreservesDiscoveryEvidenceAndAddsRelevantPageText(t *testing.T) {
	t.Parallel()
	const discovery = "Aurora telescope viewing reservations open on September 3 for the mountain observatory."
	extractor, endpoint := evidenceTestExtractor(t, `<html><head><meta property="article:published_time" content="2026-09-03"></head><body><p>The Aurora telescope offers a 40-centimeter aperture and public guided observing sessions every clear evening.</p></body></html>`)
	input := []Result{{Title: "Aurora telescope visitor guide", URL: endpoint, Snippet: discovery}}
	got, extracted := extractor.enrich(context.Background(), "Aurora telescope viewing", input)
	if extracted != 1 || len(got) != 1 {
		t.Fatalf("enrich() result count=%d extracted=%d", len(got), extracted)
	}
	if got[0].Snippet != discovery || !strings.Contains(strings.Join(got[0].PageExcerpts, "\n\n"), "40-centimeter aperture") {
		t.Errorf("enrich() must preserve separate discovery and fetched evidence: %#v", got[0])
	}
	if got[0].PagePublishedDate != "2026-09-03" || got[0].PublishedDate != "" {
		t.Errorf("enrich() must retain publication only in its fetched field: %#v", got[0])
	}
	if input[0].Snippet != discovery || input[0].PublishedDate != "" {
		t.Errorf("enrich() mutated discovery input: %#v", input[0])
	}
}

func TestPageEnrichPreservesDiscoveryPublicationDate(t *testing.T) {
	t.Parallel()
	extractor, endpoint := evidenceTestExtractor(t, `<meta itemprop="datePublished" content="2026-09-03"><p>Aurora telescope offers confirmed viewing sessions with an astronomer throughout September.</p>`)
	input := []Result{{URL: endpoint, Snippet: "Aurora telescope viewing information.", PublishedDate: "2026-09-01T12:00:00Z"}}
	got, _ := extractor.enrich(context.Background(), "Aurora telescope viewing", input)
	if got[0].PublishedDate != input[0].PublishedDate {
		t.Errorf("enrich() replaced existing publication date: got %q, want %q", got[0].PublishedDate, input[0].PublishedDate)
	}
}

func TestPageEnrichDoesNotReplaceDiscoveryWithIrrelevantFooter(t *testing.T) {
	t.Parallel()
	const discovery = "Aurora telescope viewing opens September 3 with a $12 admission price and advance reservations."
	extractor, endpoint := evidenceTestExtractor(t, `<html><body><footer>Subscribe to our newsletter. Manage your cookie preferences. Terms of service and privacy policy are available from this page.</footer></body></html>`)
	got, extracted := extractor.enrich(context.Background(), "Aurora telescope viewing", []Result{{URL: endpoint, Snippet: discovery}})
	if got[0].Snippet != discovery || extracted != 0 {
		t.Errorf("irrelevant extraction changed evidence: snippet=%q extracted=%d", got[0].Snippet, extracted)
	}
}

func TestPageEnrichBoundsEachEvidenceChannelByRunes(t *testing.T) {
	t.Parallel()
	discovery := "Aurora telescope discovery: " + strings.Repeat("星", 1300)
	extractor, endpoint := evidenceTestExtractor(t, `<p>Aurora telescope aperture is 40 centimeters.</p><p>`+strings.Repeat("Additional observing information. ", 100)+`</p>`)
	got, extracted := extractor.enrich(context.Background(), "Aurora telescope aperture", []Result{{URL: endpoint, Snippet: discovery}})
	excerpts := strings.Join(got[0].PageExcerpts, "\n\n")
	if extracted != 1 || got[0].Snippet != discovery || !strings.Contains(excerpts, "40 centimeters") {
		t.Errorf("bounded enrichment lost discovery or useful fetched evidence: extracted=%d result=%#v", extracted, got[0])
	}
	if !utf8.ValidString(got[0].Snippet) || utf8.RuneCountInString(got[0].Snippet) > maxSnippetLength || !utf8.ValidString(excerpts) || utf8.RuneCountInString(excerpts) > maxSnippetLength {
		t.Errorf("invalid UTF-8 or source budget exceeded: %#v", got[0])
	}
}

func TestQueryChunksRetainShortRelevantDateEvidence(t *testing.T) {
	t.Parallel()
	const dateLine = "Aurora launch: 2026-09-03"
	got := selectQueryChunks("Aurora telescope launch date", dateLine+"\nManage cookie preferences and website notification settings in your profile.")
	if !strings.Contains(got, dateLine) {
		t.Errorf("short relevant date line was discarded: %q", got)
	}
}

func TestPageEnrichDoesNotTruncateFullLengthDiscoveryToAppendEvidence(t *testing.T) {
	t.Parallel()
	discovery := strings.Repeat("星", maxSnippetLength-1) + "終"
	extractor, endpoint := evidenceTestExtractor(t, `<p>Aurora telescope aperture is 40 centimeters and observing reservations are available.</p>`)
	got, extracted := extractor.enrich(context.Background(), "Aurora telescope aperture", []Result{{URL: endpoint, Snippet: discovery}})
	if got[0].Snippet != discovery || extracted != 1 || len(got[0].PageExcerpts) == 0 || got[0].ExtractionStatus != extractionSucceeded {
		t.Errorf("full-length discovery changed or successful fetched evidence was lost: got %d runes extracted=%d result=%#v", utf8.RuneCountInString(got[0].Snippet), extracted, got[0])
	}
}

func TestPageEnrichCountsSuccessfulFetchedEvidence(t *testing.T) {
	t.Parallel()
	const discovery = "Aurora telescope aperture is 40 centimeters and observing reservations are available."
	for _, scenario := range []struct {
		name, metadata, wantDate string
		wantExtracted            int
	}{
		{"duplicate_text", "", "", 1},
		{"new_publication_date", `<meta itemprop="datePublished" content="2026-09-03">`, "2026-09-03", 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			extractor, endpoint := evidenceTestExtractor(t, scenario.metadata+"<p>"+discovery+"</p>")
			got, extracted := extractor.enrich(context.Background(), "Aurora telescope aperture", []Result{{URL: endpoint, Snippet: discovery}})
			if got[0].Snippet != discovery || got[0].PublishedDate != "" || extracted != scenario.wantExtracted || got[0].PagePublishedDate != scenario.wantDate || got[0].ExtractionStatus != extractionSucceeded || len(got[0].PageExcerpts) == 0 {
				t.Errorf("enrich()=%#v count=%d, want original snippet date=%q count=%d", got[0], extracted, scenario.wantDate, scenario.wantExtracted)
			}
		})
	}
}

func TestTruncateIncludesEllipsisInRuneLimit(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name  string
		value string
		limit int
		want  string
	}{
		{"negative_limit", "text", -1, ""},
		{"zero_limit", "text", 0, ""},
		{"one_rune_limit", "星空", 1, "…"},
		{"at_limit", "星空", 2, "星空"},
		{"one_over_limit", "星空夜", 2, "星…"},
		{"empty", "", 2, ""},
		{"trim_before_limit", "  星空  ", 2, "星空"},
		{"combining_unicode_validity", "e\u0301xy", 3, "e\u0301…"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			got := truncate(scenario.value, scenario.limit)
			if got != scenario.want || !utf8.ValidString(got) || utf8.RuneCountInString(got) > max(0, scenario.limit) {
				t.Errorf("truncate(%q,%d)=%q, want %q within limit", scenario.value, scenario.limit, got, scenario.want)
			}
		})
	}
}

func evidenceTestExtractor(t *testing.T, markup string) (*pageExtractor, string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, markup)
	}))
	t.Cleanup(server.Close)
	return &pageExtractor{client: server.Client(), validateURL: func(context.Context, *url.URL) error { return nil }}, server.URL
}
