package openai

import (
	"testing"
	"time"
)

func TestWebEvidenceRecurringReservationSeasonBoundariesAndYearWrap(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name, season, date string
		inside, valid      bool
	}{
		{"summer_start", "May 1 to September 30", "2026-05-01", true, true},
		{"summer_end", "May 1 to September 30", "2026-09-30", true, true},
		{"summer_before", "May 1 to September 30", "2026-04-30", false, true},
		{"summer_after", "May 1 to September 30", "2026-10-01", false, true},
		{"wrap_start", "October 1 to May 31", "2026-10-01", true, true},
		{"wrap_december", "October 1 to May 31", "2026-12-31", true, true},
		{"wrap_january", "October 1 to May 31", "2027-01-01", true, true},
		{"wrap_end", "October 1 to May 31", "2027-05-31", true, true},
		{"wrap_after", "October 1 to May 31", "2027-06-01", false, true},
		{"abbreviated_months", "Oct. 1–May 31", "2026-11-01", true, true},
		{"no_explicit_range", "During the busy season", "2026-09-03", false, false},
		{"day_zero", "May 0 to May 31", "2026-05-15", false, false},
		{"day_over_31", "May 1 to May 32", "2026-05-15", false, false},
		{"impossible_month_day", "February 31 to May 31", "2026-05-15", false, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			date, err := time.Parse("2006-01-02", scenario.date)
			if err != nil {
				t.Fatal(err)
			}
			inside, valid := inRecurringDateRange(date, evidenceMonthDayRange.FindStringSubmatch(scenario.season))
			if inside != scenario.inside || valid != scenario.valid {
				t.Errorf("inside=%t valid=%t, want inside=%t valid=%t", inside, valid, scenario.inside, scenario.valid)
			}
		})
	}
}

func TestWebEvidenceTomorrowReservationSeasonRejectsContradictionsOnly(t *testing.T) {
	t.Parallel()
	const rule = "Aurora Observatory reservations are required October 1 to May 31."
	for _, scenario := range []struct {
		name, claim, quote, query string
		wantContradiction         bool
	}{
		{"outside_yes", "Yes, tomorrow needs one.", rule, "Do I need a reservation tomorrow?", true},
		{"outside_explicit_required", "A reservation is required for tomorrow.", rule, "Do I need a reservation tomorrow?", true},
		{"outside_no", "No, you do not need a reservation under that seasonal rule.", rule, "Do I need a reservation tomorrow?", false},
		{"season_summary_without_yes", "The quoted reservation season runs October 1 to May 31.", rule, "Do I need a reservation tomorrow?", false},
		{"unrelated_query", "Yes, reservations are required.", rule, "Explain the reservation season.", false},
		{"no_reservation_query", "Yes, tomorrow needs one.", rule, "Will I need a jacket tomorrow?", false},
		{"no_quoted_season", "Yes, tomorrow needs one.", "Aurora Observatory uses a reservation system.", "Do I need a reservation tomorrow?", false},
		{"range_only_in_claim", "Yes, October 1 to May 31 means tomorrow needs one.", "Aurora Observatory uses a reservation system.", "Do I need a reservation tomorrow?", false},
		{"unrelated_date_range", "Yes, tomorrow needs one.", "Aurora hosts the comet exhibit October 1 to May 31. Reservations are required all year.", "Do I need a reservation tomorrow?", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if got := contradictsTomorrowReservationSeason(scenario.claim, scenario.quote, scenario.query, evidenceFixtureNow()); got != scenario.wantContradiction {
				t.Errorf("contradiction=%t, want %t", got, scenario.wantContradiction)
			}
		})
	}
	inside := time.Date(2026, time.December, 31, 23, 59, 0, 0, evidenceFixtureNow().Location())
	if contradictsTomorrowReservationSeason("Yes, tomorrow needs one.", rule, "Do I need a reservation tomorrow?", inside) {
		t.Error("New Year tomorrow was incorrectly placed outside the wrapped season")
	}
}

func TestWebEvidenceClaimValidationAppliesTomorrowSeasonGuard(t *testing.T) {
	t.Parallel()
	const quote = "Aurora Observatory reservations are required October 1 to May 31."
	claim := webEvidenceClaim{Text: "Yes, tomorrow needs one.", SourceURL: evidenceFixtureURL, SupportQuote: quote}
	if reason := validateEvidenceClaim(claim, evidenceFixtureSources(quote), "Do I need a reservation tomorrow?", evidenceFixtureNow()); reason == "" {
		t.Error("source-bound claim validation omitted the tomorrow-season contradiction guard")
	}
}
