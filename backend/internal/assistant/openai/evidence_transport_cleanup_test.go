package openai

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestEvidenceTransportCleanupPreservesFetchedClaimsAndDateOwnership(t *testing.T) {
	t.Parallel()
	const parentURL = "https://aurora.example/news"
	const childURL = "https://cedar.example/announcement"
	const parentQuote = "Aurora Observatory opened its telescope, the club announced Thursday."
	const childQuote = "Cedar Observatory opened its telescope, the club announced Thursday."
	const discovery = "Aurora Observatory closed its telescope, the club announced Thursday."
	const pageDate = "2026-09-03T16:00:00Z"
	beforeRows := []webEvidenceSource{
		{URL: parentURL, Title: "Aurora announcement", Snippet: discovery + "\n\n" + parentQuote,
			DiscoverySnippet: discovery, PageExcerpts: []string{parentQuote}, ExtractionStatus: "succeeded",
			PublishedDate: "2026-09-02T16:00:00Z", PagePublishedDate: pageDate},
		{URL: childURL, Title: "Cedar announcement", Snippet: childQuote,
			PageExcerpts: []string{childQuote}, ExtractionStatus: "succeeded",
			PublishedDate: pageDate, PagePublishedDate: pageDate},
	}
	afterRows := append([]webEvidenceSource(nil), beforeRows...)
	afterRows[0].Snippet = discovery
	afterRows[1].Snippet = ""
	afterRows[1].PublishedDate = "" // This fixture's child date was copied from its fetched page.
	before, after := map[string][]webEvidenceSource{}, map[string][]webEvidenceSource{}
	collectProvenanceRows(t, before, beforeRows)
	collectProvenanceRows(t, after, afterRows)
	for _, sourceURL := range []string{parentURL, childURL} {
		if !reflect.DeepEqual(eligibleEvidenceFragments(before[sourceURL]), eligibleEvidenceFragments(after[sourceURL])) {
			t.Errorf("transport cleanup changed eligible fetched passages for %s", sourceURL)
		}
	}
	if len(after[childURL]) != 1 || after[parentURL][0].PublishedDate != beforeRows[0].PublishedDate || after[childURL][0].PagePublishedDate != pageDate {
		t.Fatal("cleanup lost a linked source or changed independently owned date metadata")
	}
	for _, scenario := range []struct {
		name, sourceURL, quote string
		valid                  bool
	}{
		{"parent_fetched", parentURL, parentQuote, true},
		{"child_fetched", childURL, childQuote, true},
		{"conflicting_discovery", parentURL, discovery, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			candidate, err := json.Marshal(webEvidenceAnswer{Claims: []webEvidenceClaim{{
				Text: scenario.quote, SourceURL: scenario.sourceURL, SupportQuote: scenario.quote,
				DateQuote: scenario.quote, EventDate: "2026-09-03",
			}}, Limitations: []string{}})
			if err != nil {
				t.Fatal(err)
			}
			original, originalRejected := renderWebEvidence(string(candidate), before, "What changed today?", evidenceFixtureNow())
			cleaned, cleanedRejected := renderWebEvidence(string(candidate), after, "What changed today?", evidenceFixtureNow())
			if original != cleaned || !reflect.DeepEqual(originalRejected, cleanedRejected) {
				t.Fatalf("cleanup changed rendered claims or rejections: before=%q/%v after=%q/%v", original, originalRejected, cleaned, cleanedRejected)
			}
			if (len(cleanedRejected) == 0) != scenario.valid {
				t.Errorf("claim validity=%t want=%t, reasons=%v", len(cleanedRejected) == 0, scenario.valid, cleanedRejected)
			}
		})
	}
}
