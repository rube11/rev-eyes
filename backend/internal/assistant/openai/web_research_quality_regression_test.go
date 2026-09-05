package openai

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/tool/websearch"
)

func TestWebResearchQualityRejectsKnownFailures(t *testing.T) {
	asOf := qualityFixtureClock()
	tests := []struct {
		name       string
		scenario   string
		response   string
		results    []websearch.Result
		domains    []string
		query      string
		failedGate string
	}{
		{
			name: "wrong city restaurant with invented publisher", scenario: "las_vegas_restaurants_for_jolene",
			response:   "Try a Las Vegas noodle shop (Tripadvisor)",
			results:    []websearch.Result{{Title: "New York restaurants", URL: "https://www.opentable.com/new-york", Snippet: "Chinese restaurants in New York with pho and dim sum."}},
			failedGate: "scenario_relevant_evidence",
		},
		{
			name: "query and URL do not count as place evidence", scenario: "las_vegas_restaurants_for_jolene",
			response: "Try this place (example.com)", query: "Las Vegas ramen for two around $60",
			results:    []websearch.Result{{Title: "Vietnamese recipes", URL: "https://example.com/las-vegas", Snippet: "Learn to make pho at home."}},
			failedGate: "scenario_relevant_evidence",
		},
		{
			name: "constraints cannot be joined across unrelated sources", scenario: "las_vegas_restaurants_for_jolene",
			response: "Try these places (example.com)",
			results: []websearch.Result{
				{Title: "Las Vegas hotels", URL: "https://example.com/hotels", Snippet: "Hotels in Las Vegas."},
				{Title: "Dim sum restaurants in Miami", URL: "https://example.com/dining", Snippet: "Chinese restaurants and menus in Florida."},
			}, failedGate: "scenario_relevant_evidence",
		},
		{
			name: "requested domain violated", scenario: "las_vegas_restaurants_for_jolene",
			response: "Try a noodle shop (example.com)", results: []websearch.Result{qualityRestaurantSource()},
			domains: []string{"tripadvisor.com"}, failedGate: "returned_domains_respect_filters",
		},
		{
			name: "site filter violated", scenario: "las_vegas_restaurants_for_jolene",
			response: "Try a noodle shop (example.com)", results: []websearch.Result{qualityRestaurantSource()},
			query: "site:tripadvisor.com Las Vegas ramen", failedGate: "returned_domains_respect_filters",
		},
		{
			name: "site query cannot broaden explicit include domains", scenario: "las_vegas_restaurants_for_jolene",
			response: "Try a noodle shop (example.com)", results: []websearch.Result{qualityRestaurantSource()},
			domains: []string{"tripadvisor.com"}, query: "site:example.com Las Vegas ramen", failedGate: "returned_domains_respect_filters",
		},
		{
			name: "invented source attribution", scenario: "las_vegas_restaurants_for_jolene",
			response: "Try a noodle shop (OpenTable)", results: []websearch.Result{qualityRestaurantSource()},
			failedGate: "citation_provenance",
		},
		{
			name: "unretrieved URL on same publisher", scenario: "las_vegas_restaurants_for_jolene",
			response: "Try [the shop](https://example.com/unretrieved-page)", results: []websearch.Result{qualityRestaurantSource()},
			failedGate: "citation_provenance",
		},
		{
			name: "citation only to irrelevant retrieved page", scenario: "las_vegas_restaurants_for_jolene",
			response: "Try a noodle shop (unrelated.com)", results: []websearch.Result{
				qualityRestaurantSource(), {Title: "New York hotels", URL: "https://unrelated.com/hotels", Snippet: "Hotels in Manhattan."},
			}, failedGate: "citation_provenance",
		},
		{
			name: "wrong Red Rock park", scenario: "official_red_rock_entry_guidance",
			response: "Yes, reserve now (NPS)", results: []websearch.Result{{Title: "Red Rock Canyon", URL: "https://www.nps.gov/blca/red-rock.htm", Snippet: "A wilderness permit for Red Rock Canyon at Black Canyon of the Gunnison."}},
			failedGate: "scenario_relevant_evidence",
		},
		{
			name: "spoofed authority hostname", scenario: "official_red_rock_entry_guidance",
			response: "Reserve now (blm.gov.evil.example)", results: []websearch.Result{{Title: "Las Vegas Red Rock Canyon scenic drive reservations", URL: "https://blm.gov.evil.example/red-rock", Snippet: "Timed entry for the Nevada National Conservation Area."}},
			failedGate: "scenario_relevant_evidence",
		},
		{
			name: "same date show already past", scenario: "tonight_in_vegas_with_mateo",
			response: "A concert at 6 PM (example.com)", results: []websearch.Result{{Title: "Las Vegas live music", URL: "https://example.com/event", Snippet: "September 3, 2026. Concert at 6:00 PM. Tickets $45."}},
			failedGate: "tonight_not_past_show_evidence",
		},
		{
			name: "tomorrow show is not tonight", scenario: "tonight_in_vegas_with_mateo",
			response: "A concert at 11 PM (example.com)", results: []websearch.Result{{Title: "Las Vegas live music", URL: "https://example.com/event", Snippet: "September 4, 2026. Concert at 11:00 PM. Tickets $45."}},
			failedGate: "tonight_not_past_show_evidence",
		},
		{
			name: "Raiders navigation is not transaction evidence", scenario: "confirmed_raiders_news",
			response: "The Raiders made a move (NFL)", results: []websearch.Result{{Title: "NFL latest news", URL: "https://www.nfl.com/news", Snippet: "Teams Raiders Schedule Tickets Shop Roster Subscribe", PublishedDate: "2026-09-03"}},
			failedGate: "scenario_relevant_evidence",
		},
		{
			name: "old transaction article", scenario: "confirmed_raiders_news",
			response: "The Raiders signed a player (Raiders.com)", results: []websearch.Result{{Title: "Raiders signed a player", URL: "https://www.raiders.com/news/transaction", Snippet: "Confirmed player signing.", PublishedDate: "2025-09-03"}},
			failedGate: "recent_transaction_evidence",
		},
		{
			name: "future publication does not count", scenario: "confirmed_raiders_news",
			response: "The Raiders signed a player (Raiders.com)", results: []websearch.Result{{Title: "Raiders signed a player", URL: "https://www.raiders.com/news/transaction", Snippet: "Confirmed player signing.", PublishedDate: "2026-09-05"}},
			failedGate: "recent_transaction_evidence",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			search := qualityFixtureSearch(t, tt.results, tt.domains, tt.query)
			assessment := evaluateLiveSearchQuality(liveEyesWebScenario{name: tt.scenario}, tt.response, []liveComparisonSearch{search}, asOf)
			if assessment.MinimumGatePassed {
				t.Fatal("known bad evidence passed the minimum quality gate")
			}
			for _, check := range assessment.Checks {
				if check.Name == tt.failedGate {
					if check.Passed {
						t.Fatalf("gate %s unexpectedly passed: %s", tt.failedGate, check.Detail)
					}
					return
				}
			}
			t.Fatalf("missing expected gate %s", tt.failedGate)
		})
	}
}

func TestWebResearchQualityAcceptsEvidenceButAlwaysRequiresManualReview(t *testing.T) {
	tests := []struct {
		scenario string
		response string
		source   websearch.Result
	}{
		{"las_vegas_restaurants_for_jolene", "Fictional Noodle - Las Vegas ramen; verify total (example.com)", qualityRestaurantSource()},
		{"official_red_rock_entry_guidance", "Check the seasonal rule (BLM)", websearch.Result{Title: "Red Rock Canyon National Conservation Area", URL: "https://www.blm.gov/red-rock", Snippet: "Las Vegas, Nevada. Scenic Drive timed entry reservations and seasonal exceptions."}},
		{"tonight_in_vegas_with_mateo", "Comedy at 11 PM; verify remaining tickets (example.com)", websearch.Result{Title: "Las Vegas comedy", URL: "https://example.com/show", Snippet: "September 3, 2026 at 11:00 PM. Tickets from $40."}},
		{"confirmed_raiders_news", "The Raiders signed a player (Raiders.com)", websearch.Result{Title: "Raiders signed a player", URL: "https://www.raiders.com/news/signing", Snippet: "The club confirmed a roster move.", PublishedDate: "2026-09-02T10:00:00Z"}},
	}
	for _, tt := range tests {
		t.Run(tt.scenario, func(t *testing.T) {
			search := qualityFixtureSearch(t, []websearch.Result{tt.source}, nil, "")
			assessment := evaluateLiveSearchQuality(liveEyesWebScenario{name: tt.scenario}, tt.response, []liveComparisonSearch{search}, qualityFixtureClock())
			if !assessment.MinimumGatePassed {
				t.Fatalf("minimum evidence checks failed: %+v", assessment.Checks)
			}
			if len(assessment.ManualReviewRequired) < 2 {
				t.Fatal("heuristics must never eliminate manual claim review")
			}
		})
	}
}

func TestWebResearchQualityUnsuccessfulSearchIsNotSatisfactory(t *testing.T) {
	assessment := evaluateLiveSearchQuality(liveEyesWebScenario{name: "las_vegas_restaurants_for_jolene"},
		"I couldn't verify any restaurants.", []liveComparisonSearch{{Error: "web search returned no usable results"}}, qualityFixtureClock())
	if assessment.MinimumGatePassed {
		t.Fatal("safe abstention is not satisfactory retrieved evidence")
	}
}

func TestWebResearchQualityProvenance(t *testing.T) {
	first := qualityRestaurantSource()
	second := websearch.Result{Title: first.Title, URL: "https://second.example/restaurant", Snippet: first.Snippet}
	sources := map[string]websearch.Result{first.URL: first, second.URL: second}
	if matches := qualityResolveSourceName("Fictional Noodle", sources); len(matches) != 0 {
		t.Fatalf("ambiguous title attribution unexpectedly resolved: %v", matches)
	}
	if matches := qualityResolveSourceName("example.com", sources); len(matches) != 1 || matches[0] != first.URL {
		t.Fatalf("publisher attribution = %v, want only %s", matches, first.URL)
	}
	brandSource := websearch.Result{Title: "Comedy tickets", URL: "https://www.vividseats.com/tickets", Snippet: "Las Vegas show."}
	if matches := qualityResolveSourceName("Vivid Seats", map[string]websearch.Result{brandSource.URL: brandSource}); len(matches) != 1 {
		t.Fatalf("unambiguous multiword brand failed to match a whole DNS label: %v", matches)
	}
	brandBlog := websearch.Result{Title: "Ticket information", URL: "https://blog.vividseats.com/tickets", Snippet: "Ticket guide."}
	if matches := qualityResolveSourceName("Vivid Seats", map[string]websearch.Result{brandSource.URL: brandSource, brandBlog.URL: brandBlog}); len(matches) != 2 {
		t.Fatalf("publisher blog and www subdomains should not make a brand ambiguous: %v", matches)
	}
	lookalike := websearch.Result{Title: "Ticket information", URL: "https://vividseats.evil.example/tickets", Snippet: "Ticket guide."}
	if matches := qualityResolveSourceName("Vivid Seats", map[string]websearch.Result{brandSource.URL: brandSource, lookalike.URL: lookalike}); len(matches) != 0 {
		t.Fatalf("lookalike publisher suffixes should remain ambiguous: %v", matches)
	}
	vegasSource := websearch.Result{Title: "Vegas shows", URL: "https://www.vegas.com/shows", Snippet: "Vegas shows."}
	vegasTopic := websearch.Result{Title: "Vegas shows", URL: "https://www.ticketmaster.com/shows", Snippet: "Vegas shows."}
	if matches := qualityResolveSourceName("Vegas.com", map[string]websearch.Result{vegasSource.URL: vegasSource, vegasTopic.URL: vegasTopic}); len(matches) != 1 || matches[0] != vegasSource.URL {
		t.Fatalf("literal publisher host must not match an unrelated title plus TLD: %v", matches)
	}
	if names := qualityResponseSourceNames("According to Raiders.com, a move occurred. Sources: BLM / recreation.gov."); len(names) != 3 {
		t.Fatalf("inline domain and labeled source names = %v", names)
	}
	if names := qualityResponseSourceNames("Source: Raiders.com. Also, NFL.com reported a trade."); len(names) != 2 {
		t.Fatalf("source labels should not swallow prose: %v", names)
	}
	if names := qualityResponseSourceNames("Comedy show (ticket price not verified) (ticketmaster.com)"); len(names) != 1 || names[0] != "ticketmaster.com" {
		t.Fatalf("an explicit uncertainty caveat is not an invented publisher: %v", names)
	}
	if qualityURLInDomains("https://notexample.com/a", []string{"example.com"}) || qualityURLInDomains("https://example.com.evil/a", []string{"example.com"}) {
		t.Fatal("suffix lookalike passed requested-domain check")
	}
	if !qualityURLInDomains("https://www.example.com/a", []string{"example.com"}) {
		t.Fatal("real subdomain rejected")
	}
	urls := qualityResponseURLs("Read [this source](https://example.com/restaurant#menu). https://example.com/restaurant.")
	if len(urls) != 1 || urls[0] != first.URL {
		t.Fatalf("canonical citation URLs = %v", urls)
	}
}

func TestWebResearchQualityRecognizesExplicitTicketDateFormats(t *testing.T) {
	for _, date := range []string{"2026-09-03", "September 3, 2026", "Sep 3, 2026", "9/3/26", "09/03/2026"} {
		t.Run(date, func(t *testing.T) {
			source := websearch.Result{Title: "Las Vegas comedy", Snippet: date + " 11:00 PM"}
			if !qualityHasUpcomingShowtime(source, qualityFixtureClock()) {
				t.Fatalf("explicit US-market same-day future showtime rejected: %q", source.Snippet)
			}
		})
	}
}

func TestWebResearchQualityRecurringActivityWindows(t *testing.T) {
	// The clock is Thursday at 9:30pm in Las Vegas. All fixtures are synthetic;
	// they express evidence requirements, not schedules for real venues.
	tests := []struct {
		name    string
		title   string
		snippet string
		want    bool
	}{
		{"daily overnight", "Las Vegas live music", "Free live music daily from 6pm to 1am.", true},
		{"nightly overnight unicode dash", "Las Vegas live music", "Live music nightly 6 p.m.–1 a.m.", true},
		{"current weekday", "Las Vegas comedy", "Comedy every Thursday from 10pm until midnight.", true},
		{"current weekday plural", "Las Vegas live music", "Live music Thursdays 6pm–11pm.", true},
		{"future daily start", "Las Vegas live music", "Live music daily 10pm-1am.", true},
		{"past ending", "Las Vegas live music", "Live music nightly 6pm-9pm.", false},
		{"wrong weekday", "Las Vegas comedy", "Comedy every Friday 10pm-midnight.", false},
		{"start without end", "Las Vegas live music", "Live music nightly beginning at 6pm.", false},
		{"no recurrence", "Las Vegas live music", "Live music 6pm-1am.", false},
		{"month only show", "Las Vegas comedy", "Comedy in September 2026 at 11pm.", false},
		{"stale explicit year", "Las Vegas live music", "Live music nightly during September 2025 from 6pm-1am.", false},
		{"stale title year", "Las Vegas live music schedule 2025", "Live music daily 6pm-1am.", false},
		{"wrong seasonal month", "Las Vegas live music", "Live music nightly in August from 6pm-1am.", false},
		{"old date restriction", "Las Vegas live music", "Live music daily through 2026-08-31 from 6pm-1am.", false},
		{"exception needs manual interpretation", "Las Vegas live music", "Live music daily except Thursdays 6pm-1am.", false},
		{"unrelated shop window", "Las Vegas guide", "Live music nightly 6pm-9pm; the souvenir shop is open daily 10pm-1am.", false},
		{"unrelated cross sentence window", "Las Vegas guide", "Live music nightly 6pm-9pm. The museum opens daily 10pm-1am.", false},
		{"unrelated chunk date", "Las Vegas live music", "Museum exhibition 2026-09-03\n\nConcert 2026-09-04 at 11pm.", false},
		{"canopy hours are not live music hours", "Las Vegas entertainment", "Free canopy shows occur daily from 6pm-2am. Live music on weekends can begin at noon and continue until 2am.", false},
		{"weekend restriction overrides generic daily label", "Las Vegas live music", "Daily live music events are offered, with weekend live music running from 6pm-2am.", false},
		{"tentative hours are not an operating window", "Las Vegas live music", "Live music nightly can begin as early as 6pm-2am.", false},
		{"recorded music is not live performance", "Las Vegas live music", "The canopy shows feature live music videos nightly 6pm-2am.", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := websearch.Result{Title: tt.title, Snippet: tt.snippet, URL: "https://example.com/schedule"}
			if got := qualityHasUpcomingShowtime(source, qualityFixtureClock()); got != tt.want {
				t.Fatalf("remaining activity window = %v, want %v; %q", got, tt.want, tt.snippet)
			}
		})
	}
}

func TestWebResearchQualityCalendarWeekIsNotRollingSevenDays(t *testing.T) {
	asOf := qualityFixtureClock() // Thursday, September 3; current week began Monday, August 31.
	for _, tt := range []struct {
		name      string
		published string
		inWeek    bool
	}{
		{"previous Sunday", "2026-08-30", false},
		{"local Sunday but UTC Monday", "2026-08-31T06:59:00Z", false},
		{"local Monday midnight", "2026-08-31T07:00:00Z", true},
		{"Tuesday", "2026-09-01", true},
		{"future Friday", "2026-09-04", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := websearch.Result{Title: "Raiders signed a player", URL: "https://www.raiders.com/news/signing", Snippet: "The club confirmed a roster move.", PublishedDate: tt.published}
			if got := qualityHasCurrentWeekSourceDate(source, asOf); got != tt.inWeek {
				t.Fatalf("calendar-week evidence = %v, want %v for %s", got, tt.inWeek, tt.published)
			}
			search := qualityFixtureSearch(t, []websearch.Result{source}, nil, "")
			assessment := evaluateLiveSearchQuality(liveEyesWebScenario{name: "confirmed_raiders_news"},
				"This week, the Raiders signed a player (Raiders.com)", []liveComparisonSearch{search}, asOf)
			if check := qualityFixtureCheck(t, assessment, "current_calendar_week_source_evidence"); check.Passed != tt.inWeek {
				t.Fatalf("explicit this-week answer gate = %+v", check)
			}
		})
	}
	sunday := websearch.Result{PublishedDate: "2026-08-30"}
	if !qualityRecentlyPublished(sunday, asOf) || qualityHasCurrentWeekSourceDate(sunday, asOf) {
		t.Fatal("Sunday must be recent within seven days but outside the current Monday-start week")
	}
}

func TestWebResearchQualityPastSevenDaysDoesNotClaimCalendarWeek(t *testing.T) {
	source := websearch.Result{Title: "Raiders signed a player", URL: "https://www.raiders.com/news/signing", Snippet: "The club confirmed a roster move.", PublishedDate: "2026-08-30"}
	search := qualityFixtureSearch(t, []websearch.Result{source}, nil, "")
	assessment := evaluateLiveSearchQuality(liveEyesWebScenario{name: "confirmed_raiders_news"},
		"In the past seven days, the Raiders signed a player (Raiders.com)", []liveComparisonSearch{search}, qualityFixtureClock())
	for _, check := range assessment.Checks {
		if check.Name == "current_calendar_week_source_evidence" {
			t.Fatal("do not silently convert an explicit rolling window into a calendar-week claim")
		}
	}
	if !assessment.MinimumGatePassed || len(assessment.ManualReviewRequired) == 0 {
		t.Fatalf("rolling-window source should clear screening but still require event-date review: %+v", assessment)
	}
}

func TestWebResearchQualityGlassesLengthCountsRunesAndDoesNotTruncate(t *testing.T) {
	for _, count := range []int{0, 420, 421} {
		response := strings.Repeat("👓", count)
		assessment := evaluateLiveSearchQuality(liveEyesWebScenario{name: "las_vegas_restaurants_for_jolene"}, response, nil, qualityFixtureClock())
		check := qualityFixtureCheck(t, assessment, "response_fits_glasses")
		if check.Passed != (count > 0 && count <= 420) {
			t.Fatalf("length gate for %d Unicode characters = %+v", count, check)
		}
		if response != strings.Repeat("👓", count) {
			t.Fatal("quality screening must not silently truncate or rewrite the answer")
		}
	}
}

// These are human-labeled NEGATIVE claim/evidence pairs, not passing factual
// examples. Quote containment and source identity cannot resolve their meaning.
// Keep them executable as diagnostics without freezing known production false
// passes into desired behavior: a future verifier may correctly reject more of
// them. Until then, screening must always retain manual entailment review.
func TestWebResearchQualitySemanticCounterexamplesRequireManualReview(t *testing.T) {
	tests := []struct {
		name       string
		scenario   string
		query      string
		source     websearch.Result
		claim      webEvidenceClaim
		rejectWhy  string
		failedGate string
	}{
		{
			name: "same block bands cannot borrow canopy schedule", scenario: "tonight_in_vegas_with_mateo", query: "Las Vegas live music tonight",
			source:     websearch.Result{Title: "Las Vegas live music", URL: "https://example.com/music", Snippet: "Live bands perform in downtown Las Vegas. Canopy light shows run nightly from 6pm to 2am."},
			claim:      webEvidenceClaim{Text: "Live bands have a remaining performance window tonight.", SupportQuote: "Live bands perform in downtown Las Vegas.", ScheduleQuote: "Canopy light shows run nightly from 6pm to 2am.", StartsAt: "2026-09-03T18:00:00-07:00", EndsAt: "2026-09-04T02:00:00-07:00"},
			rejectWhy:  "The clocks describe canopy light shows, not the live bands; sharing one paragraph does not transfer an activity's schedule.",
			failedGate: "tonight_not_past_show_evidence",
		},
		{
			name: "Monday restaurant hours generalized to Thursday", scenario: "las_vegas_restaurants_for_jolene", query: "Las Vegas ramen restaurants for dinner",
			source:    websearch.Result{Title: "Noodle restaurant Las Vegas", URL: "https://example.com/noodles", Snippet: "Noodle serves ramen in Las Vegas. Monday hours: 8am to 2:30am."},
			claim:     webEvidenceClaim{Text: "Noodle is open Thursday for a late dinner.", SupportQuote: "Noodle serves ramen in Las Vegas. Monday hours: 8am to 2:30am."},
			rejectWhy: "The source only supplies Monday hours. The fixture clock is Thursday; an undated restaurant query does not authorize dropping that weekday qualifier.",
		},
		{
			name: "reservation negation reversed", scenario: "official_red_rock_entry_guidance", query: "Red Rock Canyon Scenic Drive reservation tomorrow morning",
			source:    websearch.Result{Title: "Red Rock Canyon Scenic Drive in Nevada", URL: "https://www.blm.gov/example", Snippet: "Timed entry reservations are not required for the Red Rock Canyon Scenic Drive in September."},
			claim:     webEvidenceClaim{Text: "A timed-entry reservation is required for the Scenic Drive in September.", SupportQuote: "Timed entry reservations are not required for the Red Rock Canyon Scenic Drive in September."},
			rejectWhy: "The affirmative reservation claim directly reverses the source's not-required statement.",
		},
		{
			name: "no delivery fee claim reverses not free source", scenario: "las_vegas_restaurants_for_jolene", query: "Las Vegas ramen restaurants",
			source:    websearch.Result{Title: "Noodle Las Vegas ramen menu", URL: "https://example.com/noodles", Snippet: "Noodle is a Las Vegas ramen restaurant. Delivery is not free."},
			claim:     webEvidenceClaim{Text: "Noodle offers free delivery.", SupportQuote: "Noodle is a Las Vegas ramen restaurant. Delivery is not free."},
			rejectWhy: "Removing not reverses the delivery-fee statement; there are no numeric tokens to protect this claim.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.claim.SourceURL = tt.source.URL
			rows := map[string][]webEvidenceSource{tt.source.URL: {{URL: tt.source.URL, Title: tt.source.Title, Snippet: tt.source.Snippet}}}
			if !sourceContainsQuote(rows[tt.source.URL], tt.claim.SupportQuote) || tt.rejectWhy == "" {
				t.Fatal("semantic-negative fixture must have real same-source quotations and an explicit human rejection rationale")
			}
			if reason := validateEvidenceClaim(tt.claim, rows, tt.query, qualityFixtureClock()); reason == "" {
				t.Logf("KNOWN SEMANTIC FALSE PASS in source-bound validation; human verdict REJECT: %s", tt.rejectWhy)
			} else {
				t.Logf("Source-bound validation rejected this human-negative pair (%s); human rationale remains: %s", reason, tt.rejectWhy)
			}
			response := tt.claim.Text + " (" + strings.TrimPrefix(strings.Split(tt.source.URL, "/")[2], "www.") + ")"
			search := qualityFixtureSearch(t, []websearch.Result{tt.source}, nil, tt.query)
			assessment := evaluateLiveSearchQuality(liveEyesWebScenario{name: tt.scenario}, response, []liveComparisonSearch{search}, qualityFixtureClock())
			if !strings.Contains(strings.Join(assessment.ManualReviewRequired, " "), "every concrete answer claim is entailed") {
				t.Fatal("mechanical screening must not remove explicit manual entailment review for semantic counterexamples")
			}
			if tt.failedGate != "" && qualityFixtureCheck(t, assessment, tt.failedGate).Passed {
				t.Fatalf("known retrieval-level negative must still fail %s; semantic caveats do not excuse weakening existing gates", tt.failedGate)
			}
			if assessment.MinimumGatePassed {
				t.Logf("Minimum retrieval/provenance screening passed, but human verdict remains REJECT: %s", tt.rejectWhy)
			}
		})
	}
}

func TestWebResearchQualityReservationSeasonPolarityIsSourceDerived(t *testing.T) {
	location, _ := time.LoadLocation("America/Los_Angeles")
	for _, tt := range []struct {
		name, asOf, season, response string
		passed                       bool
	}{
		{"replay1 affirmative outside season", "2026-09-03", "October 1 - May 31", "Yes—timed-entry reservations are required Oct 1–May 31, so tomorrow morning needs one. (blm.gov)", false},
		{"correct outside-season no", "2026-09-03", "October 1 - May 31", "No—tomorrow morning does not need a reservation. (blm.gov)", true},
		{"yes during different summer season", "2026-09-03", "June 1 through September 30", "Yes, tomorrow morning needs a reservation. (blm.gov)", true},
		{"no hardcoded September exemption", "2026-10-01", "June 1 through September 30", "You need a reservation tomorrow morning. (blm.gov)", false},
		{"inclusive winter start", "2026-09-30", "Oct. 1–May 31", "Yes, reserve for tomorrow. (blm.gov)", true},
		{"inclusive winter end", "2026-05-30", "October 1 to May 31", "Yes, tomorrow morning needs one. (blm.gov)", true},
		{"day after winter end", "2026-05-31", "October 1 to May 31", "Yes, tomorrow morning needs one. (blm.gov)", false},
		{"winter crosses year", "2026-12-31", "Oct 1 - May 31", "Yes, tomorrow morning needs one. (blm.gov)", true},
		{"different day boundary", "2026-08-20", "May 10 - August 20", "Tomorrow morning needs a reservation. (blm.gov)", false},
		{"source-only rule statement is not a current yes", "2026-09-03", "October 1 - May 31", "The season is October through May; check morning entry details. (blm.gov)", true},
		{"quoted requirement is not affirmative tomorrow inference", "2026-09-03", "October 1 - May 31", "Timed entry reservations are required October 1 through May 31. (blm.gov)", true},
		{"qualified before-hours no left for manual review", "2026-10-03", "October 1 - May 31", "No reservation is needed if you enter before 8am. (blm.gov)", true},
		{"malformed calendar day cannot form season", "2026-09-03", "February 31 - May 31", "Yes, tomorrow morning needs one. (blm.gov)", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			asOf, err := time.ParseInLocation("2006-01-02", tt.asOf, location)
			if err != nil {
				t.Fatal(err)
			}
			source := websearch.Result{Title: "Red Rock Canyon Scenic Drive Nevada", URL: "https://www.blm.gov/example", Snippet: "Timed entry reservations are required for the Scenic Drive between " + tt.season + " for entry between 8am and 5pm."}
			assessment := evaluateLiveSearchQuality(liveEyesWebScenario{name: "official_red_rock_entry_guidance"}, tt.response,
				[]liveComparisonSearch{qualityFixtureSearch(t, []websearch.Result{source}, nil, "Red Rock Canyon reservation tomorrow morning")}, asOf)
			check := qualityFixtureCheck(t, assessment, "seasonal_reservation_answer_consistency")
			if check.Passed != tt.passed {
				t.Fatalf("passed=%v, want %v: %s", check.Passed, tt.passed, check.Detail)
			}
			if !tt.passed && assessment.MinimumGatePassed {
				t.Fatal("season contradiction did not fail the aggregate gate")
			}
			if len(assessment.ManualReviewRequired) == 0 {
				t.Fatal("season checks do not replace manual rule interpretation")
			}
		})
	}
}

func TestWebResearchQualitySourceLabelCannotConsumeFollowingProseLine(t *testing.T) {
	for _, lineBreak := range []string{"\n", "\r\n"} {
		response := "From the retrieved sources:" + lineBreak + "1. Noodle Asia offers dim sum, noodle dishes, and pho. (noodleasiavenetian.com)"
		names := qualityResponseSourceNames(response)
		if len(names) != 1 || names[0] != "noodleasiavenetian.com" {
			t.Fatalf("header label swallowed prose into publisher aliases: %v", names)
		}
	}
	// A real unsupported source remains a citation; do not fix header parsing
	// by silently ignoring unrecognized publisher names.
	names := qualityResponseSourceNames("From the retrieved sources:\nNoodles are affordable (Invented Publisher)")
	if len(names) != 1 || names[0] != "Invented Publisher" {
		t.Fatalf("unsupported named citation was hidden: %v", names)
	}
}

func TestWebResearchQualityReservationSeasonStaysWithCitedOfficialSource(t *testing.T) {
	winter := websearch.Result{Title: "Red Rock Canyon Scenic Drive Nevada", URL: "https://www.blm.gov/winter", Snippet: "Timed entry reservations are required for the Scenic Drive October 1 - May 31."}
	summer := websearch.Result{Title: winter.Title, URL: "https://www.recreation.gov/summer", Snippet: "Timed entry reservations are required for the Scenic Drive June 1 - September 30."}
	sources := map[string]websearch.Result{winter.URL: winter, summer.URL: summer}
	if !qualitySeasonalReservationCheck("Yes, tomorrow needs a reservation. (https://www.recreation.gov/summer)", sources, qualityFixtureClock()).Passed {
		t.Fatal("uncited other source's season must not override the quoted official source")
	}
	if qualitySeasonalReservationCheck("Yes, tomorrow needs a reservation. (https://www.blm.gov/winter)", sources, qualityFixtureClock()).Passed {
		t.Fatal("an uncited conflicting source must not rescue an explicit contradiction")
	}
	unbound := winter
	unbound.Snippet = "Timed entry reservations are required for the Scenic Drive. The unrelated exhibition season is October 1 - May 31."
	if !qualitySeasonalReservationCheck("Yes, tomorrow needs a reservation. (blm.gov)", map[string]websearch.Result{unbound.URL: unbound}, qualityFixtureClock()).Passed {
		t.Fatal("requirement text must not borrow an unrelated sentence's season; this claim remains for manual review")
	}
}

func qualityFixtureCheck(t *testing.T, assessment liveQualityAssessment, name string) liveQualityCheck {
	t.Helper()
	for _, check := range assessment.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("assessment is missing check %q", name)
	return liveQualityCheck{}
}

func qualityFixtureClock() time.Time {
	location, _ := time.LoadLocation("America/Los_Angeles")
	return time.Date(2026, 9, 3, 21, 30, 0, 0, location)
}

func qualityRestaurantSource() websearch.Result {
	return websearch.Result{Title: "Fictional Noodle", URL: "https://example.com/restaurant", Snippet: "A Las Vegas ramen restaurant. Menu bowls $18 each."}
}

func qualityFixtureSearch(t *testing.T, results []websearch.Result, domains []string, query string) liveComparisonSearch {
	t.Helper()
	arguments, err := json.Marshal(liveSearchArguments{Query: query, IncludeDomains: domains})
	if err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(struct {
		Provider string             `json:"provider"`
		Results  []websearch.Result `json:"results"`
	}{Provider: "searxng", Results: results})
	if err != nil {
		t.Fatal(err)
	}
	return liveComparisonSearch{Arguments: arguments, Content: string(content)}
}
