package openai

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const evidenceFixtureURL = "https://aurora.example/observatory"

func evidenceFixtureNow() time.Time {
	return time.Date(2026, time.September, 3, 22, 0, 0, 0, time.FixedZone("PDT", -7*3600))
}

func evidenceFixtureSources(snippet string) map[string][]webEvidenceSource {
	return map[string][]webEvidenceSource{evidenceFixtureURL: {{URL: evidenceFixtureURL, Title: "Aurora Observatory", Snippet: snippet}}}
}

func TestWebEvidenceRejectsUnknownSourcesForgedQuotesAndMismatchedNumbers(t *testing.T) {
	t.Parallel()
	const quote = "Aurora telescope admission costs $18 per adult."
	base := webEvidenceClaim{Text: "Aurora telescope admission costs $18.", SourceURL: evidenceFixtureURL, SupportQuote: quote}
	for _, scenario := range []struct {
		name string
		edit func(*webEvidenceClaim)
	}{
		{"unknown_url", func(c *webEvidenceClaim) { c.SourceURL = "https://other.example/observatory" }},
		{"url_must_be_exact", func(c *webEvidenceClaim) { c.SourceURL += "?different-source" }},
		{"forged_quote", func(c *webEvidenceClaim) { c.SupportQuote = "Aurora telescope admission is always free." }},
		{"wrong_source_price", func(c *webEvidenceClaim) { c.Text = "Aurora telescope admission costs $28." }},
		{"forged_date_quote", func(c *webEvidenceClaim) { c.DateQuote = "On September 3, 2026, admission changed." }},
		{"forged_schedule_quote", func(c *webEvidenceClaim) { c.ScheduleQuote = "Open daily from 8pm to 11pm." }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			claim := base
			scenario.edit(&claim)
			sources := evidenceFixtureSources(quote)
			sources["https://other.example/prices"] = []webEvidenceSource{{URL: "https://other.example/prices", Snippet: "Aurora telescope admission costs $28."}}
			if reason := validateEvidenceClaim(claim, sources, "Find admission prices", evidenceFixtureNow()); reason == "" {
				t.Error("invalid source-bound claim was accepted")
			}
		})
	}
	if reason := validateEvidenceClaim(base, evidenceFixtureSources(quote), "Find admission prices", evidenceFixtureNow()); reason != "" {
		t.Errorf("exact supported source/quote/price rejected: %s", reason)
	}
}

func TestWebEvidencePriceCannotBorrowUnrelatedNumbers(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct{ name, quote, text string }{
		{"age_is_not_price", "Aurora admission costs $28; minimum age is 18.", "Aurora admission costs $18."},
		{"thousands_component_is_not_price", "Aurora private tours cost $1,200 per group.", "Aurora private tours cost $200."},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			claim := webEvidenceClaim{Text: scenario.text, SourceURL: evidenceFixtureURL, SupportQuote: scenario.quote}
			if reason := validateEvidenceClaim(claim, evidenceFixtureSources(scenario.quote), "What does admission cost?", evidenceFixtureNow()); reason == "" {
				t.Error("price was inferred from an unrelated or partial number")
			}
		})
	}
}

func TestWebEvidenceClockTypographyPreservesSupportedNumbers(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct{ name, quote, text string }{
		{"compact_source_spaced_claim", "Aurora telescope visits run from 8am to 5pm.", "Aurora telescope visits run from 8 a.m. to 5 p.m."},
		{"spaced_source_compact_claim", "Aurora telescope visits run from 8 a.m. to 5 p.m.", "Aurora telescope visits run from 8am to 5pm."},
		{"hyphenated_clock_range", "Aurora telescope visits run 8am -5pm.", "Aurora telescope visits run 8 a.m.–5 p.m."},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			claim := webEvidenceClaim{Text: scenario.text, SourceURL: evidenceFixtureURL, SupportQuote: scenario.quote}
			if reason := validateEvidenceClaim(claim, evidenceFixtureSources(scenario.quote), "What are the visiting hours?", evidenceFixtureNow()); reason != "" {
				t.Errorf("equivalent clock typography was rejected: %s", reason)
			}
		})
	}
}

func TestWebEvidenceRelativeAnnouncementDateRequiresSameSourceAndCalendarDay(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name, dateQuote, published string
		wantValid                  bool
	}{
		{"same_day_announcement", "Aurora Observatory opened its telescope, the club announced Thursday.", "2026-09-03T16:00:00Z", true},
		{"same_day_offset_announcement", "Aurora Observatory opened its telescope, the club announced on Thursday.", "2026-09-03T12:00:00-07:00", true},
		{"wrong_publication_day", "Aurora Observatory opened its telescope, the club announced Thursday.", "2026-09-02T16:00:00Z", false},
		{"utc_local_date_disagreement", "Aurora Observatory opened its telescope, the club announced Thursday.", "2026-09-04T02:00:00Z", false},
		{"previous_local_date", "Aurora Observatory opened its telescope, the club announced Thursday.", "2026-09-03T02:00:00Z", false},
		{"date_only_publication", "Aurora Observatory opened its telescope, the club announced Thursday.", "2026-09-03", false},
		{"no_publication", "Aurora Observatory opened its telescope, the club announced Thursday.", "", false},
		{"last_weekday", "Aurora Observatory opened its telescope, the club announced last Thursday.", "2026-09-03T16:00:00Z", false},
		{"next_weekday", "Aurora Observatory opened its telescope, the club announced next Thursday.", "2026-09-03T16:00:00Z", false},
		{"wrong_weekday", "Aurora Observatory opened its telescope, the club announced Tuesday.", "2026-09-03T16:00:00Z", false},
		{"metadata_only", "Aurora Observatory opened its telescope. Published Thursday.", "2026-09-03T16:00:00Z", false},
		{"future_not_past_event", "Aurora Observatory telescope will be opened Thursday.", "2026-09-03T16:00:00Z", false},
		{"negated_event", "Aurora Observatory telescope was not opened Thursday.", "2026-09-03T16:00:00Z", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			claim := webEvidenceClaim{Text: "Aurora Observatory telescope opened.", SourceURL: evidenceFixtureURL, SupportQuote: scenario.dateQuote, DateQuote: scenario.dateQuote, EventDate: "2026-09-03"}
			sources := map[string][]webEvidenceSource{evidenceFixtureURL: {{URL: evidenceFixtureURL, Snippet: scenario.dateQuote, PublishedDate: scenario.published}}}
			reason := validateEvidenceClaim(claim, sources, "What changed today?", evidenceFixtureNow())
			if (reason == "") != scenario.wantValid {
				t.Errorf("valid=%t reason=%q, want valid=%t", reason == "", reason, scenario.wantValid)
			}
		})
	}
}

func TestWebEvidenceRelativeAnnouncementCannotBorrowPublicationAcrossVersions(t *testing.T) {
	t.Parallel()
	const dated = "Aurora Observatory opened its telescope, the club announced Thursday."
	claim := webEvidenceClaim{Text: "Aurora Observatory opened its telescope.", SourceURL: evidenceFixtureURL, SupportQuote: "Aurora Observatory opened its telescope", DateQuote: dated, EventDate: "2026-09-03"}
	for _, scenario := range []struct {
		name string
		rows []webEvidenceSource
	}{
		{"separate_source_version", []webEvidenceSource{{URL: evidenceFixtureURL, Snippet: dated}, {URL: evidenceFixtureURL, Snippet: "A different page version describes a cafe.", PublishedDate: "2026-09-03T16:00:00Z"}}},
		{"separate_fragment", []webEvidenceSource{{URL: evidenceFixtureURL, Snippet: "Aurora Observatory opened its telescope.\n\nThe club announced Thursday.", PublishedDate: "2026-09-03T16:00:00Z"}}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			candidate := claim
			if scenario.name == "separate_fragment" {
				candidate.DateQuote = "The club announced Thursday."
			}
			sources := map[string][]webEvidenceSource{evidenceFixtureURL: scenario.rows, "https://other.example/story": {{URL: "https://other.example/story", Snippet: dated, PublishedDate: "2026-09-03T16:00:00Z"}}}
			if reason := validateEvidenceClaim(candidate, sources, "What changed this week?", evidenceFixtureNow()); reason == "" {
				t.Error("relative event borrowed another version/source/fragment's publication metadata")
			}
		})
	}
}

func TestWebEvidenceEventDatesRequireIndividualCurrentWindowEvidence(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name, query, eventDate, dateQuote, snippet string
		wantValid                                  bool
	}{
		{"current_week", "What changed this week?", "2026-09-03", "On September 3, 2026, Aurora Observatory opened its telescope.", "On September 3, 2026, Aurora Observatory opened its telescope.", true},
		{"previous_week", "What changed this week?", "2026-08-30", "On August 30, 2026, Aurora Observatory opened its telescope.", "On August 30, 2026, Aurora Observatory opened its telescope.", false},
		{"future_this_week", "What changed this week?", "2026-09-04", "On September 4, 2026, Aurora Observatory opened its telescope.", "On September 4, 2026, Aurora Observatory opened its telescope.", false},
		{"publication_only_same_fragment", "What changed this week?", "2026-09-03", "Published September 3, 2026.", "Published September 3, 2026. Aurora Observatory opened its telescope.", false},
		{"separate_date_fragment", "What changed this week?", "2026-09-03", "On September 3, 2026, another update appeared.", "Aurora Observatory opened its telescope.\n\nOn September 3, 2026, another update appeared.", false},
		{"today", "What changed today?", "2026-09-03", "On September 3, 2026, Aurora Observatory opened its telescope.", "On September 3, 2026, Aurora Observatory opened its telescope.", true},
		{"today_cannot_use_yesterday", "What changed today?", "2026-09-02", "On September 2, 2026, Aurora Observatory opened its telescope.", "On September 2, 2026, Aurora Observatory opened its telescope.", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			claim := webEvidenceClaim{Text: "Aurora Observatory opened its telescope.", SourceURL: evidenceFixtureURL, SupportQuote: "Aurora Observatory opened its telescope.", EventDate: scenario.eventDate, DateQuote: scenario.dateQuote}
			reason := validateEvidenceClaim(claim, evidenceFixtureSources(scenario.snippet), scenario.query, evidenceFixtureNow())
			if (reason == "") != scenario.wantValid {
				t.Errorf("valid=%t reason=%q, want valid=%t", reason == "", reason, scenario.wantValid)
			}
		})
	}
}

func TestWebEvidenceTonightRequiresSameActivityClocksAndApplicableRecurrence(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name, schedule, start, end string
		now                        time.Time
		split                      bool
		wantValid                  bool
	}{
		{"daily_remaining_window", "Aurora telescope viewing is available daily from 8pm to 11pm.", "2026-09-03T20:00:00-07:00", "2026-09-03T23:00:00-07:00", evidenceFixtureNow(), false, true},
		{"explicit_weekday", "Aurora telescope viewing is available Thursday from 8pm to 11pm.", "2026-09-03T20:00:00-07:00", "2026-09-03T23:00:00-07:00", evidenceFixtureNow(), false, true},
		{"valid_weekend", "Aurora telescope viewing is available weekends from 8pm to 11pm.", "2026-09-05T20:00:00-07:00", "2026-09-05T23:00:00-07:00", evidenceFixtureNow().AddDate(0, 0, 2), false, true},
		{"weekend_on_weekday", "Aurora telescope viewing is available weekends from 8pm to 11pm.", "2026-09-03T20:00:00-07:00", "2026-09-03T23:00:00-07:00", evidenceFixtureNow(), false, false},
		{"wrong_named_weekday", "Aurora telescope viewing is available Friday and Saturday nightly from 8pm to 11pm.", "2026-09-03T20:00:00-07:00", "2026-09-03T23:00:00-07:00", evidenceFixtureNow(), false, false},
		{"weekday_exception", "Aurora telescope viewing is available daily except Thursday from 8pm to 11pm.", "2026-09-03T20:00:00-07:00", "2026-09-03T23:00:00-07:00", evidenceFixtureNow(), false, false},
		{"explicit_day_negation", "Aurora telescope viewing is available daily from 8pm to 11pm. No Thursday shows.", "2026-09-03T20:00:00-07:00", "2026-09-03T23:00:00-07:00", evidenceFixtureNow(), false, false},
		{"wrong_explicit_date", "Aurora telescope viewing is available September 2, 2026 from 8pm to 11pm.", "2026-09-03T20:00:00-07:00", "2026-09-03T23:00:00-07:00", evidenceFixtureNow(), false, false},
		{"fully_past_window", "Aurora telescope viewing is available daily from 6pm to 9pm.", "2026-09-03T18:00:00-07:00", "2026-09-03T21:00:00-07:00", evidenceFixtureNow(), false, false},
		{"missing_end_clock", "Aurora telescope viewing is available daily starting at 8pm.", "2026-09-03T20:00:00-07:00", "2026-09-03T23:00:00-07:00", evidenceFixtureNow(), false, false},
		{"cross_fragment_clocks", "The cafe is open daily from 8pm to 11pm.", "2026-09-03T20:00:00-07:00", "2026-09-03T23:00:00-07:00", evidenceFixtureNow(), true, false},
		{"missing_offsets", "Aurora telescope viewing is available daily from 8pm to 11pm.", "2026-09-03T20:00:00", "2026-09-03T23:00:00", evidenceFixtureNow(), false, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			snippet := scenario.schedule
			if scenario.split {
				snippet = "Aurora telescope viewing is available.\n\n" + scenario.schedule
			}
			claim := webEvidenceClaim{Text: "Aurora telescope viewing is available.", SourceURL: evidenceFixtureURL, SupportQuote: "Aurora telescope viewing is available", ScheduleQuote: scenario.schedule, StartsAt: scenario.start, EndsAt: scenario.end}
			reason := validateEvidenceClaim(claim, evidenceFixtureSources(snippet), "What can I do tonight?", scenario.now)
			if (reason == "") != scenario.wantValid {
				t.Errorf("valid=%t reason=%q, want valid=%t", reason == "", reason, scenario.wantValid)
			}
		})
	}
}

func TestRenderWebEvidenceBindsHostAndDropsWholeClaimsForGlassesLimit(t *testing.T) {
	t.Parallel()
	sources := make(map[string][]webEvidenceSource)
	answer := webEvidenceAnswer{Limitations: []string{"prices_unverified", "availability_unverified"}}
	for _, name := range []string{"Aurora", "Borealis", "Celestial"} {
		text := name + " telescope " + strings.Repeat("星", 145) + " complete."
		endpoint := fmt.Sprintf("https://www.%s.example/info", strings.ToLower(name))
		sources[endpoint] = []webEvidenceSource{{URL: endpoint, Snippet: text}}
		answer.Claims = append(answer.Claims, webEvidenceClaim{Text: text, SourceURL: endpoint, SupportQuote: text})
	}
	raw, _ := json.Marshal(answer)
	response, _ := renderWebEvidence(string(raw), sources, "Find telescope options", evidenceFixtureNow())
	if utf8.RuneCountInString(response) > 420 || !strings.Contains(response, "(aurora.example)") || strings.Contains(response, "www.") {
		t.Errorf("rendered response violates length/actual-host contract: %q", response)
	}
	for _, claim := range answer.Claims {
		if strings.Contains(response, strings.Fields(claim.Text)[0]+" telescope") && !strings.Contains(response, claim.Text) {
			t.Errorf("renderer cut a claim instead of dropping it: %q", response)
		}
	}
	if !strings.Contains(response, "Current all-in prices unverified.") || !strings.Contains(response, "Availability unverified.") {
		t.Errorf("required caveats lost: %q", response)
	}
}

func TestWebEvidenceShortQuoteCannotHideFullFragmentScheduleRestrictions(t *testing.T) {
	t.Parallel()
	const schedule = "Aurora telescope viewing is available daily from 8pm to 11pm."
	for _, qualification := range []string{
		"Summer 2025 program.",
		"July program only.",
		"Except Thursdays.",
		"No Thursday shows.",
		"Friday and Saturday program only.",
		"Subject to weather.",
	} {
		t.Run(qualification, func(t *testing.T) {
			claim := webEvidenceClaim{
				Text: "Aurora telescope viewing is available.", SourceURL: evidenceFixtureURL,
				SupportQuote: "Aurora telescope viewing is available", ScheduleQuote: schedule,
				StartsAt: "2026-09-03T20:00:00-07:00", EndsAt: "2026-09-03T23:00:00-07:00",
			}
			if reason := validateEvidenceClaim(claim, evidenceFixtureSources(schedule+" "+qualification), "Find an activity tonight.", evidenceFixtureNow()); reason == "" {
				t.Errorf("short quote bypassed full-fragment restriction %q", qualification)
			}
		})
	}
}

func TestCollectWebEvidenceOnlyAcceptsSearXNGSearchRowsAndBoundsSources(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name, toolName, provider string
		want                     int
	}{
		{"searxng_search", "search_web", "searxng", 1},
		{"tavily_search", "search_web", "tavily", 0},
		{"missing_provider", "search_web", "", 0},
		{"other_tool", "lookup", "searxng", 0},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			content, _ := json.Marshal(map[string]any{"provider": scenario.provider, "results": []webEvidenceSource{{URL: evidenceFixtureURL, Snippet: "Aurora telescope opens daily."}, {URL: "javascript:alert(1)", Snippet: "Invalid source"}, {URL: "https://user:password@bad.example", Snippet: "Credentials URL"}}})
			output, _ := json.Marshal(toolOutput{Type: "function_call_output", CallID: "one", Output: string(content)})
			sources := make(map[string][]webEvidenceSource)
			collectWebEvidence(sources, []toolCall{{Name: scenario.toolName, CallID: "one"}}, []json.RawMessage{output})
			if len(sources) != scenario.want {
				t.Errorf("collected=%d want=%d", len(sources), scenario.want)
			}
		})
	}
	var rows []webEvidenceSource
	for index := 0; index < 50; index++ {
		rows = append(rows, webEvidenceSource{URL: fmt.Sprintf("https://aurora.example/%d", index), Snippet: "Aurora telescope evidence."})
	}
	content, _ := json.Marshal(map[string]any{"provider": "searxng", "results": rows})
	output, _ := json.Marshal(toolOutput{Type: "function_call_output", CallID: "one", Output: string(content)})
	sources := make(map[string][]webEvidenceSource)
	collectWebEvidence(sources, []toolCall{{Name: "search_web", CallID: "one"}}, []json.RawMessage{output})
	if len(sources) != 40 {
		t.Errorf("source count=%d, want bound40", len(sources))
	}
}

func TestCollectWebEvidencePreservesPublicationMetadataPerVersion(t *testing.T) {
	t.Parallel()
	rows := []webEvidenceSource{
		{URL: evidenceFixtureURL, Snippet: "Aurora Observatory opened Thursday.", PublishedDate: "2026-09-03T16:00:00Z"},
		{URL: evidenceFixtureURL, Snippet: "Aurora Observatory had an older exhibit.", PublishedDate: "2026-08-03T16:00:00Z"},
	}
	content, _ := json.Marshal(map[string]any{"provider": "searxng", "results": rows})
	output, _ := json.Marshal(toolOutput{Type: "function_call_output", CallID: "one", Output: string(content)})
	sources := make(map[string][]webEvidenceSource)
	collectWebEvidence(sources, []toolCall{{Name: "search_web", CallID: "one"}}, []json.RawMessage{output})
	if len(sources[evidenceFixtureURL]) != 2 {
		t.Fatalf("source versions=%d, want two", len(sources[evidenceFixtureURL]))
	}
	for index, got := range sources[evidenceFixtureURL] {
		if got.PublishedDate != rows[index].PublishedDate || got.Snippet != rows[index].Snippet {
			t.Errorf("source publication metadata mixed across versions: %#v", sources)
		}
	}
}
