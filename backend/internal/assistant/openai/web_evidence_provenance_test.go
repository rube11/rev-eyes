package openai

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestEvidenceProvenanceFetchedBlocksOverrideDiscoveryTitleAndFlatSnippet(t *testing.T) {
	const discovery = "Cinder Mesa Park opens daily from 9am to 4pm."
	const park = "Section: Cinder Mesa Park > Park grounds\nThe park grounds open daily from 6am to 10pm."
	const center = "Section: Cinder Mesa Park > Visitor center\nThe visitor center opens daily from 9am to 4pm."
	rows := []webEvidenceSource{
		{URL: evidenceFixtureURL, Title: "An older title claims admission is free.", Snippet: discovery},
		{URL: evidenceFixtureURL, Title: "A fetched search title claims admission is free.", Snippet: discovery + "\n\nFlat-only text claims overnight camping is free.", DiscoverySnippet: discovery, PageExcerpts: []string{park, center}, ExtractionStatus: "succeeded"},
	}
	for _, test := range []struct {
		quote string
		valid bool
	}{
		{park, true}, {center, true},
		{"The park grounds open daily from 6am to 10pm.", true},
		{discovery, false},
		{"An older title claims admission is free.", false},
		{"A fetched search title claims admission is free.", false},
		{"Flat-only text claims overnight camping is free.", false},
	} {
		if got := sourceContainsQuote(rows, test.quote); got != test.valid {
			t.Errorf("quote eligibility=%t want=%t for %q", got, test.valid, test.quote)
		}
	}
	claim := webEvidenceClaim{Text: discovery, SourceURL: evidenceFixtureURL, SupportQuote: discovery}
	if reason := validateEvidenceClaim(claim, map[string][]webEvidenceSource{evidenceFixtureURL: rows}, "Find park operating hours", evidenceFixtureNow()); reason == "" {
		t.Error("ambiguous discovery acquired fetched authority merely because the URL was fetched")
	}
}

func TestEvidenceProvenanceCannotSpliceAcrossFetchedBlocksOrVersions(t *testing.T) {
	const left = "Aurora telescope viewing is available."
	const right = "Viewing is available daily from 8pm to 11pm."
	for _, rows := range [][]webEvidenceSource{
		{{URL: evidenceFixtureURL, PageExcerpts: []string{left, right}, ExtractionStatus: "succeeded", Snippet: left + " " + right}},
		{{URL: evidenceFixtureURL, PageExcerpts: []string{left}, ExtractionStatus: "succeeded"}, {URL: evidenceFixtureURL, PageExcerpts: []string{right}, ExtractionStatus: "succeeded"}},
	} {
		if !sourceContainsQuote(rows, left) || !sourceContainsQuote(rows, right) {
			t.Fatal("individually fetched passages should remain eligible")
		}
		for _, combined := range []string{left + " " + right, left + "\n\n" + right, left + " ... " + right} {
			if sourceContainsQuote(rows, combined) {
				t.Errorf("separate fetched blocks accepted a fabricated combined quote: %q", combined)
			}
		}
		if quotesShareFragment(rows, left, right) || commonEvidenceFragment(rows, left, right) != "" {
			t.Error("separate fetched passages became one factual fragment")
		}
		claim := webEvidenceClaim{Text: left, SourceURL: evidenceFixtureURL, SupportQuote: left, ScheduleQuote: right, StartsAt: "2026-09-03T20:00:00-07:00", EndsAt: "2026-09-03T23:00:00-07:00"}
		if reason := validateEvidenceClaim(claim, map[string][]webEvidenceSource{evidenceFixtureURL: rows}, "What can I do tonight?", evidenceFixtureNow()); reason == "" {
			t.Error("schedule borrowed another fetched fragment's activity context")
		}
	}
	contiguous := []webEvidenceSource{{URL: evidenceFixtureURL, PageExcerpts: []string{"Section: Aurora viewing\n" + left + " " + right}, ExtractionStatus: "succeeded"}}
	if !sourceContainsQuote(contiguous, left+" "+right) || !quotesShareFragment(contiguous, left, right) {
		t.Fatal("one actual contiguous fetched passage should remain eligible")
	}
}

func TestEvidenceProvenanceNoFetchedBlocksRejectsDiscoveryButPreservesGenuineLegacy(t *testing.T) {
	const discovery = "Aurora telescope offers public guided viewing."
	const legacy = "Legacy snippet describes a different package."
	for _, status := range []string{"not_requested", "unavailable", "succeeded"} {
		rows := []webEvidenceSource{{URL: evidenceFixtureURL, DiscoverySnippet: discovery, Snippet: legacy, ExtractionStatus: status, PageExcerpts: []string{"", "  "}}}
		if sourceContainsQuote(rows, discovery) || sourceContainsQuote(rows, legacy) {
			t.Errorf("status %q promoted discovery or stale flat text without fetched blocks", status)
		}
	}
	oldRows := []webEvidenceSource{{URL: evidenceFixtureURL, Title: "Legacy Aurora source title", Snippet: discovery}}
	if !sourceContainsQuote(oldRows, discovery) || !sourceContainsQuote(oldRows, "Legacy Aurora source title") {
		t.Fatal("ordinary legacy capture compatibility was lost")
	}
	claim := webEvidenceClaim{Text: discovery, SourceURL: evidenceFixtureURL, SupportQuote: discovery}
	if reason := validateEvidenceClaim(claim, map[string][]webEvidenceSource{evidenceFixtureURL: oldRows}, "Find public observing information", evidenceFixtureNow()); reason != "" {
		t.Fatalf("legacy mechanical validation failed: %s", reason)
	}
}

func TestEvidenceProvenanceCollectorRetainsFetchedUpdateAtCaptureCaps(t *testing.T) {
	const old = "Old discovery says the park closes at 4pm."
	const fetched = "Section: Park grounds\nThe park closes at 10pm."
	for _, filledURLs := range []int{1, 40} {
		t.Run(fmt.Sprintf("existing_urls_%d", filledURLs), func(t *testing.T) {
			sources := map[string][]webEvidenceSource{}
			for index := 0; index < 4; index++ {
				collectProvenanceRows(t, sources, []webEvidenceSource{{URL: evidenceFixtureURL, DiscoverySnippet: old + fmt.Sprint(index), ExtractionStatus: "not_requested"}})
			}
			for index := 1; index < filledURLs; index++ {
				endpoint := fmt.Sprintf("https://other-%d.example/source", index)
				sources[endpoint] = []webEvidenceSource{{URL: endpoint, Snippet: "Other discovery evidence."}}
			}
			// At the URL cap an unknown URL must not cause the collector to stop
			// before processing a later update to an already captured URL.
			collectProvenanceRows(t, sources, []webEvidenceSource{
				{URL: "https://new-source.example/ignored-at-cap", Snippet: "Additional discovery evidence."},
				{URL: evidenceFixtureURL, PageExcerpts: []string{fetched}, ExtractionStatus: "succeeded"},
			})
			if !sourceContainsQuote(sources[evidenceFixtureURL], fetched) || sourceContainsQuote(sources[evidenceFixtureURL], old) {
				t.Fatalf("later fetched evidence was dropped or older discovery retained authority: %#v", sources[evidenceFixtureURL])
			}
			if len(sources) > 40 || len(sources[evidenceFixtureURL]) > 4 {
				t.Fatalf("retaining a useful update exceeded collection bounds: URLs=%d versions=%d", len(sources), len(sources[evidenceFixtureURL]))
			}
		})
	}
}

func TestEvidenceProvenanceFetchedUpdateOverridesDiscoveryRegardlessOfRowOrder(t *testing.T) {
	const discovery = "Old discovery says the park closes at 4pm."
	const fetched = "Section: Park grounds\nThe park closes at 10pm."
	old := webEvidenceSource{URL: evidenceFixtureURL, DiscoverySnippet: discovery, ExtractionStatus: "unavailable"}
	page := webEvidenceSource{URL: evidenceFixtureURL, PageExcerpts: []string{fetched}, ExtractionStatus: "succeeded"}
	for _, rows := range [][]webEvidenceSource{{old, page}, {page, old}} {
		if sourceContainsQuote(rows, discovery) || !sourceContainsQuote(rows, fetched) {
			t.Fatalf("row ordering changed fetched-over-discovery precedence: %#v", rows)
		}
	}
}

func TestEvidenceProvenanceRelativeDateRequiresFetchedPublicationMetadata(t *testing.T) {
	const quote = "Aurora Observatory opened its telescope, the club announced Thursday."
	const date = "2026-09-03T16:00:00Z"
	claim := webEvidenceClaim{Text: "Aurora Observatory opened its telescope.", SourceURL: evidenceFixtureURL, SupportQuote: quote, DateQuote: quote, EventDate: "2026-09-03"}
	for _, test := range []struct {
		name  string
		rows  []webEvidenceSource
		valid bool
	}{
		{"fetched_metadata", []webEvidenceSource{{URL: evidenceFixtureURL, PageExcerpts: []string{quote}, ExtractionStatus: "succeeded", PagePublishedDate: date}}, true},
		{"inherited_discovery_date", []webEvidenceSource{{URL: evidenceFixtureURL, PageExcerpts: []string{quote}, ExtractionStatus: "succeeded", PublishedDate: date}}, false},
		{"old_discovery_version", []webEvidenceSource{{URL: evidenceFixtureURL, DiscoverySnippet: quote, PublishedDate: date, ExtractionStatus: "not_requested"}, {URL: evidenceFixtureURL, PageExcerpts: []string{quote}, ExtractionStatus: "succeeded"}}, false},
		{"other_fetched_version_date", []webEvidenceSource{{URL: evidenceFixtureURL, PageExcerpts: []string{quote}, ExtractionStatus: "succeeded"}, {URL: evidenceFixtureURL, PageExcerpts: []string{"A separate cafe changed its opening hours."}, ExtractionStatus: "succeeded", PagePublishedDate: date}}, false},
		{"wrong_fetched_date", []webEvidenceSource{{URL: evidenceFixtureURL, PageExcerpts: []string{quote}, ExtractionStatus: "succeeded", PublishedDate: date, PagePublishedDate: "2026-09-02T16:00:00Z"}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			reason := validateEvidenceClaim(claim, map[string][]webEvidenceSource{evidenceFixtureURL: test.rows}, "What changed today?", evidenceFixtureNow())
			if (reason == "") != test.valid {
				t.Fatalf("fetched relative-date validity=%t want=%t reason=%q", reason == "", test.valid, reason)
			}
		})
	}
}

func TestEvidenceProvenanceCollectorAndRepairRetainIndependentFields(t *testing.T) {
	row := webEvidenceSource{URL: evidenceFixtureURL, Title: "Aurora source", Snippet: "Combined legacy text.", DiscoverySnippet: "Unverified discovery lead.", PageExcerpts: []string{"Section: Aurora\nVerified source passage."}, ExtractionStatus: "succeeded", PublishedDate: "2020-01-01", PagePublishedDate: "2026-09-03"}
	sources := map[string][]webEvidenceSource{}
	collectProvenanceRows(t, sources, []webEvidenceSource{row})
	if !reflect.DeepEqual(sources[evidenceFixtureURL], []webEvidenceSource{row}) {
		t.Fatalf("collector dropped provenance fields: %#v", sources)
	}
	candidate, _ := json.Marshal(webEvidenceAnswer{Claims: []webEvidenceClaim{{SourceURL: evidenceFixtureURL, SupportQuote: "FABRICATED CANDIDATE TEXT"}}})
	raw := webEvidenceRepairSources(string(candidate), sources)
	var repaired []webEvidenceSource
	if json.Unmarshal([]byte(raw), &repaired) != nil || !reflect.DeepEqual(repaired, []webEvidenceSource{row}) || strings.Contains(raw, "FABRICATED CANDIDATE TEXT") {
		t.Fatalf("repair evidence changed provenance or imported candidate content: %s", raw)
	}
}

func collectProvenanceRows(t *testing.T, sources map[string][]webEvidenceSource, rows []webEvidenceSource) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"provider": "searxng", "results": rows})
	if err != nil {
		t.Fatal(err)
	}
	output, err := json.Marshal(map[string]any{"output": string(payload)})
	if err != nil {
		t.Fatal(err)
	}
	collectWebEvidence(sources, []toolCall{{Name: "search_web"}}, []json.RawMessage{output})
}
