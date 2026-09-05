package openai

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/tool/websearch"
)

func TestQualityCaptureProvenanceUsesFetchedFactsWithoutDiscoveryContamination(t *testing.T) {
	source := websearch.Result{
		URL: "https://example.com/menu", Title: "Las Vegas ramen from $5",
		Snippet: "Las Vegas ramen restaurant menu from $5.", DiscoverySnippet: "Las Vegas ramen restaurant menu from $5.",
		PageExcerpts: []string{"Section: Las Vegas ramen restaurant menu\nRamen bowls from $18."}, ExtractionStatus: "succeeded",
	}
	if !qualityRelevantSource("las_vegas_restaurants_for_jolene", source) {
		t.Fatal("fetched place and cuisine evidence was discarded")
	}
	sources := map[string]websearch.Result{source.URL: source}
	for _, test := range []struct {
		response string
		passed   bool
	}{
		{"Ramen bowls from $18 (example.com)", true},
		{"Ramen bowls from $5 (example.com)", false},
	} {
		if got := qualityQuotedPriceCitationCheck(test.response, sources); got.Passed != test.passed {
			t.Errorf("%q: %+v", test.response, got)
		}
	}
	source.PageExcerpts = []string{"Las Vegas hotels and resorts.", "Ramen restaurant menu in Miami from $18."}
	if qualityRelevantSource("las_vegas_restaurants_for_jolene", source) {
		t.Fatal("discovery title or separate fetched blocks supplied missing place/cuisine evidence")
	}
}

func TestQualityCaptureProvenanceUnavailablePagesDoNotVerifyDiscovery(t *testing.T) {
	for _, status := range []string{"unavailable", "not_requested", "succeeded"} {
		source := websearch.Result{
			URL: "https://example.com/show", Title: "Las Vegas comedy September 3, 2026",
			Snippet:          "Las Vegas comedy from $18, September 3, 2026 at 11 PM.",
			DiscoverySnippet: "Las Vegas comedy from $18, September 3, 2026 at 11 PM.",
			PageExcerpts:     []string{"", "  "}, PublishedDate: "2026-09-03", PagePublishedDate: "2026-09-03", ExtractionStatus: status,
		}
		if qualityRelevantSource("tonight_in_vegas_with_mateo", source) || qualityHasUpcomingShowtime(source, qualityFixtureClock()) || qualityRecentlyPublished(source, qualityFixtureClock()) {
			t.Errorf("%s capture without fetched text verified discovery facts", status)
		}
		if qualityQuotedPriceCitationCheck("Comedy from $18 (example.com)", map[string]websearch.Result{source.URL: source}).Passed {
			t.Errorf("%s capture without fetched text verified a price", status)
		}
	}
}

func TestQualityCaptureProvenanceKeepsEventDatesWithinFetchedBlocks(t *testing.T) {
	source := websearch.Result{URL: "https://example.com/show", Title: "Las Vegas comedy September 3, 2026",
		Snippet: "September 3, 2026 at 11 PM.", DiscoverySnippet: "September 3, 2026 at 11 PM.", ExtractionStatus: "succeeded"}
	for _, test := range []struct {
		blocks []string
		passed bool
	}{
		{[]string{"Las Vegas comedy at 11 PM."}, false},
		{[]string{"Las Vegas comedy September 3, 2026.", "A different show starts at 11 PM."}, false},
		{[]string{"Las Vegas comedy September 3, 2026 at 11 PM."}, true},
	} {
		source.PageExcerpts = test.blocks
		if qualityHasUpcomingShowtime(source, qualityFixtureClock()) != test.passed {
			t.Errorf("showtime/date provenance was lost for %#v", test.blocks)
		}
	}
}

func TestQualityCaptureProvenanceUsesPagePublicationDate(t *testing.T) {
	source := websearch.Result{URL: "https://raiders.com/news/signing", Title: "Raiders signed a player September 3, 2026",
		Snippet: "Raiders signed a player September 3, 2026.", DiscoverySnippet: "Raiders signed a player September 3, 2026.",
		PublishedDate: "2026-09-03", PageExcerpts: []string{"The Raiders confirmed a player signing."}, ExtractionStatus: "succeeded"}
	for _, test := range []struct {
		pageDate string
		passed   bool
	}{
		{"", false}, {"2025-09-03", false}, {"2026-09-03T16:00:00Z", true},
	} {
		source.PagePublishedDate = test.pageDate
		if qualityRecentlyPublished(source, qualityFixtureClock()) != test.passed || qualityHasCurrentWeekSourceDate(source, qualityFixtureClock()) != test.passed {
			t.Errorf("discovery date contaminated fetched publication %q", test.pageDate)
		}
	}
	source.PagePublishedDate = ""
	source.PageExcerpts = []string{"The Raiders confirmed a player signing September 3, 2026."}
	if !qualityRecentlyPublished(source, qualityFixtureClock()) {
		t.Fatal("explicit date in fetched text was discarded")
	}
}

func TestQualityCaptureProvenanceReadsFetchedSeasonAndRetainsFetchedUpdate(t *testing.T) {
	source := websearch.Result{URL: "https://www.blm.gov/red-rock", Title: "Red Rock Canyon scenic drive",
		Snippet:          "Las Vegas Red Rock Canyon Scenic Drive timed entry reservations are required January 1 through December 31.",
		DiscoverySnippet: "Las Vegas Red Rock Canyon Scenic Drive timed entry reservations are required January 1 through December 31.",
		PageExcerpts:     []string{"Section: Red Rock Canyon National Conservation Area, Las Vegas\nScenic Drive timed entry reservations are required October 1 through May 31."}, ExtractionStatus: "succeeded"}
	if got := qualitySeasonalReservationCheck("Yes, you need a reservation tomorrow (BLM)", map[string]websearch.Result{source.URL: source}, qualityFixtureClock()); got.Passed {
		t.Fatalf("fetched season was discarded in favor of discovery: %+v", got)
	}
	discovery := source
	discovery.PageExcerpts, discovery.ExtractionStatus = nil, "unavailable"
	for _, rows := range [][]websearch.Result{{source, discovery}, {discovery, source}} {
		searches := []liveComparisonSearch{qualityFixtureSearch(t, []websearch.Result{rows[0]}, nil, ""), qualityFixtureSearch(t, []websearch.Result{rows[1]}, nil, "")}
		assessment := evaluateLiveSearchQuality(liveEyesWebScenario{name: "official_red_rock_entry_guidance"}, "Yes, you need a reservation tomorrow (BLM)", searches, qualityFixtureClock())
		for _, check := range assessment.Checks {
			if check.Name == "seasonal_reservation_answer_consistency" && check.Passed {
				t.Fatalf("capture order displaced fetched evidence: %+v", check)
			}
		}
	}
}

func TestProviderParityProvenanceSurvivesSourceOnlyFixtureRoundTrip(t *testing.T) {
	first := map[string]any{"url": "https://aurora.example/info", "title": "Aurora", "snippet": "Discovery lead.",
		"discovery_snippet": "Discovery lead.", "published_date": "2025-01-01", "page_excerpts": []string{"Section: Telescope\nOpens at 8am."}, "page_published_date": "2026-09-03", "extraction_status": "succeeded"}
	second := map[string]any{"url": "https://aurora.example/info", "title": "Aurora", "snippet": "Discovery lead.",
		"discovery_snippet": "Discovery lead.", "published_date": "2025-01-01", "page_excerpts": []string{"Section: Telescope\nOpens at 9am."}, "page_published_date": "2026-09-03", "extraction_status": "succeeded"}
	linked := map[string]any{"url": "https://aurora.example/linked", "page_excerpts": []string{"Fetched linked page."}, "extraction_status": "succeeded"}
	malformed := map[string]any{"url": "https://aurora.example/bad", "title": "Malformed", "page_excerpts": []int{42}}
	input := parityTestData(t, parityTestRun(t, "aurora", "searxng_only", "searxng", "Aurora", []any{first, second, linked, malformed}))
	capture, err := loadProviderParityCapture(strings.NewReader(input), "fixture.json", "searxng", "searxng_only", nil)
	if err != nil {
		t.Fatal(err)
	}
	scenario := capture.Scenarios[0]
	if len(scenario.Sources) != 3 || scenario.MalformedRowCount != 1 || scenario.Metadata.WithPageExcerpts != 3 || scenario.Metadata.WithPagePublicationDate != 2 {
		t.Fatalf("fetched variants, linked source or malformed field accounting lost: %+v", scenario)
	}
	fixture := paritySourceFixture(scenario.Sources)
	for _, expected := range []map[string]any{first, second, linked} {
		raw, _ := json.Marshal(expected)
		var want webEvidenceSource
		if err := json.Unmarshal(raw, &want); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, got := range fixture {
			found = found || reflect.DeepEqual(got, want)
		}
		if !found {
			t.Fatalf("source-only fixture stripped provenance: want %+v, got %+v", want, fixture)
		}
	}
	var bytes int
	for _, source := range fixture {
		bytes += paritySourceTextBytes(source)
	}
	if scenario.DistinctSourceTextBytes != bytes || scenario.Searches[0].SourceTextBytes != bytes {
		t.Fatal("source text accounting omitted provenance fields")
	}
}
