package openai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUnavailableEvidenceFinalizerRetainsSupportedClaimButRejectsDiscoveryFees(t *testing.T) {
	const feeURL = "https://aurora.example/fees"
	const guideURL = "https://aurora.example/guide"
	const fee = "Aurora park entrance costs $8 per visitor."
	const guide = "The park visitor center opens daily."
	answer := webEvidenceAnswer{Claims: []webEvidenceClaim{
		{Text: fee, SourceURL: feeURL, SupportQuote: fee},
		{Text: guide, SourceURL: guideURL, SupportQuote: guide},
	}, Limitations: []string{}}
	raw, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	legacy := webEvidenceSource{URL: feeURL, Title: fee, Snippet: fee}
	for _, test := range []struct {
		name string
		row  webEvidenceSource
	}{
		{"unavailable", webEvidenceSource{ExtractionStatus: "unavailable"}},
		{"quick", webEvidenceSource{ExtractionStatus: "not_requested"}},
		{"empty_succeeded", webEvidenceSource{ExtractionStatus: "succeeded", PageExcerpts: []string{"", "  "}}},
		{"preserved_discovery", webEvidenceSource{DiscoverySnippet: fee}},
		{"empty_excerpts_array", webEvidenceSource{PageExcerpts: []string{}}},
		{"page_date_only", webEvidenceSource{PagePublishedDate: "2026-09-03"}},
		{"linked_child_only", webEvidenceSource{DiscoveredFrom: guideURL}},
	} {
		t.Run(test.name, func(t *testing.T) {
			row := test.row
			row.URL, row.Title, row.Snippet = feeURL, fee, fee
			for _, group := range [][]webEvidenceSource{{row}, {legacy, row}, {row, legacy}} {
				sources := map[string][]webEvidenceSource{
					feeURL:   group,
					guideURL: {{URL: guideURL, PageExcerpts: []string{"Section: Visitor center\n" + guide}, ExtractionStatus: "succeeded"}},
				}
				response, rejected := renderWebEvidence(string(raw), sources, "Verify park admission fees and visitor center information.", evidenceFixtureNow())
				if strings.Contains(response, fee) || !strings.Contains(response, guide) || len(rejected) != 1 || !strings.Contains(rejected[0], "support_quote is absent") {
					t.Fatalf("discovery fee escaped rejection or supported partial was lost: response=%q rejected=%v", response, rejected)
				}
				if review := webEvidenceRepairSources(string(raw), sources); strings.Contains(review, fee) || !strings.Contains(review, guide) {
					t.Fatalf("review re-presented an ineligible URL group or lost eligible evidence: %s", review)
				}
			}
		})
	}
	for _, group := range [][]webEvidenceSource{
		{legacy},
		{legacy, {URL: feeURL, ExtractionStatus: "unavailable"}, {URL: feeURL, PageExcerpts: []string{"Section: Park admission\n" + fee}, ExtractionStatus: "succeeded"}},
	} {
		sources := map[string][]webEvidenceSource{feeURL: group, guideURL: {{URL: guideURL, Snippet: guide}}}
		response, rejected := renderWebEvidence(string(raw), sources, "Verify park admission fees and visitor center information.", evidenceFixtureNow())
		if !strings.Contains(response, fee) || !strings.Contains(response, guide) || len(rejected) != 0 {
			t.Fatalf("genuine legacy or fetched positive control failed: response=%q rejected=%v", response, rejected)
		}
	}
}

func TestUnavailableEvidenceCollectorKeepsExplicitEmptyMetadataAcrossCaptureCap(t *testing.T) {
	const quote = "Aurora park entrance costs $8 per visitor."
	for _, key := range []string{"extraction_status", "discovery_snippet", "page_excerpts", "page_published_date", "discovered_from"} {
		for _, null := range []bool{false, true} {
			name := key
			if null {
				name += "_null"
			}
			t.Run(name, func(t *testing.T) {
				sources := make(map[string][]webEvidenceSource)
				legacy := webEvidenceSource{URL: evidenceFixtureURL, Title: quote, Snippet: quote}
				for index := 0; index < 4; index++ {
					collectProvenanceRows(t, sources, []webEvidenceSource{legacy})
				}
				if !sourceContainsQuote(sources[evidenceFixtureURL], quote) {
					t.Fatal("legacy positive control failed before provenance arrived")
				}
				var value any = ""
				if key == "page_excerpts" {
					value = []string{}
				}
				if null {
					value = nil
				}
				// A failed fetch may carry only its URL and status. It still
				// invalidates old discovery eligibility for that same URL.
				payload, _ := json.Marshal(map[string]any{"provider": "searxng", "results": []any{map[string]any{"url": evidenceFixtureURL, key: value}}})
				output, _ := json.Marshal(toolOutput{Output: string(payload)})
				collectWebEvidence(sources, []toolCall{{Name: "search_web"}}, []json.RawMessage{output})
				for index := 0; index < 8; index++ {
					collectProvenanceRows(t, sources, []webEvidenceSource{legacy})
				}
				if sourceContainsQuote(sources[evidenceFixtureURL], quote) || len(sources[evidenceFixtureURL]) > 4 {
					t.Fatalf("empty provenance disappeared at the source-version cap: %+v", sources[evidenceFixtureURL])
				}
				answer, _ := json.Marshal(webEvidenceAnswer{Claims: []webEvidenceClaim{{Text: quote, SourceURL: evidenceFixtureURL, SupportQuote: quote}}, Limitations: []string{}})
				response, rejected := renderWebEvidence(string(answer), sources, "Verify admission fees.", evidenceFixtureNow())
				if response != webEvidenceAbstention("Verify admission fees.") || len(rejected) == 0 || webEvidenceRepairSources(string(answer), sources) != "" {
					t.Fatalf("explicit empty provenance became factual support after ingestion: %q %v", response, rejected)
				}
			})
		}
	}
}
