package openai

import "testing"

func TestWebEvidenceFutureDatedPerformanceCanOmitUnknownEndTime(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct{ name, quote, start string }{
		{"explicit_local_date_and_start", "Aurora Observatory presents an evening telescope lecture on September 3, 2026 at 11pm.", "2026-09-03T23:00:00-07:00"},
		{"iso_local_structured_event", "Aurora Observatory presents an evening telescope lecture. Event startDate: 2026-09-03T23:00:00-07:00.", "2026-09-03T23:00:00-07:00"},
		{"equivalent_utc_structured_event", "Aurora Observatory presents an evening telescope lecture. Event startDate: 2026-09-04T06:00:00Z.", "2026-09-03T23:00:00-07:00"},
		{"explicit_post_midnight_remaining_night", "Aurora Observatory presents an evening telescope lecture on September 4, 2026 at 12:30am.", "2026-09-04T00:30:00-07:00"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			claim := webEvidenceClaim{Text: "Aurora Observatory presents an evening telescope lecture.", SourceURL: evidenceFixtureURL,
				SupportQuote: "Aurora Observatory presents an evening telescope lecture", ScheduleQuote: scenario.quote, StartsAt: scenario.start}
			if reason := validateEvidenceClaim(claim, evidenceFixtureSources(scenario.quote), "What can I do tonight?", evidenceFixtureNow()); reason != "" {
				t.Errorf("future dated performance should not require a fabricated end: %s", reason)
			}
		})
	}
}

func TestWebEvidenceFuturePerformanceRejectsWrongDatesUnavailableOrUnboundClocks(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct{ name, quote, start string }{
		{"already_started", "Aurora Observatory presents an evening telescope lecture on September 3, 2026 at 9pm.", "2026-09-03T21:00:00-07:00"},
		{"starts_now_not_future", "Aurora Observatory presents an evening telescope lecture on September 3, 2026 at 10pm.", "2026-09-03T22:00:00-07:00"},
		{"next_week", "Aurora Observatory presents an evening telescope lecture on September 10, 2026 at 11pm.", "2026-09-10T23:00:00-07:00"},
		{"next_day_daytime", "Aurora Observatory presents an evening telescope lecture on September 4, 2026 at 8am.", "2026-09-04T08:00:00-07:00"},
		{"next_day_evening", "Aurora Observatory presents an evening telescope lecture on September 4, 2026 at 8pm.", "2026-09-04T20:00:00-07:00"},
		{"source_wrong_day", "Aurora Observatory presents an evening telescope lecture on September 2, 2026 at 11pm.", "2026-09-03T23:00:00-07:00"},
		{"source_wrong_weekday", "Aurora Observatory presents an evening telescope lecture on Friday at 11pm.", "2026-09-03T23:00:00-07:00"},
		{"cancelled", "Aurora Observatory presents an evening telescope lecture on September 3, 2026 at 11pm. This performance is cancelled.", "2026-09-03T23:00:00-07:00"},
		{"sold_out", "Aurora Observatory presents an evening telescope lecture on September 3, 2026 at 11pm. Tickets are sold out.", "2026-09-03T23:00:00-07:00"},
		{"sold_out_hyphenated", "Aurora Observatory presents an evening telescope lecture on September 3, 2026 at 11pm. This is a sold-out performance.", "2026-09-03T23:00:00-07:00"},
		{"iso_clock_different_instant", "Aurora Observatory presents an evening telescope lecture. Event startDate: 2026-09-03T23:00:00Z.", "2026-09-03T23:00:00-07:00"},
		{"venue_24_hour_clock_not_event", "Aurora Observatory presents an evening telescope lecture on September 3, 2026. Venue hours are 17:00–23:00; the lecture start time is not listed.", "2026-09-03T23:00:00-07:00"},
		{"all_day_venue_not_event", "Aurora Observatory presents an evening telescope lecture on September 3, 2026. The venue is open 24 hours, from 00:00 to 23:59; no performance start is provided.", "2026-09-03T23:59:00-07:00"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			claim := webEvidenceClaim{Text: "Aurora Observatory presents an evening telescope lecture.", SourceURL: evidenceFixtureURL,
				SupportQuote: "Aurora Observatory presents an evening telescope lecture", ScheduleQuote: scenario.quote, StartsAt: scenario.start}
			if reason := validateEvidenceClaim(claim, evidenceFixtureSources(scenario.quote), "What can I do tonight?", evidenceFixtureNow()); reason == "" {
				t.Error("invalid future-show evidence was accepted without a supported remaining window")
			}
		})
	}
}

func TestWebEvidenceOngoingOrRecurringActivitiesStillRequireSupportedEndTime(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name, quote, start, end string
		wantValid               bool
	}{
		{"ongoing_with_no_end", "Aurora telescope viewing is available daily from 8pm.", "2026-09-03T20:00:00-07:00", "", false},
		{"ongoing_invented_end", "Aurora telescope viewing is available daily from 8pm.", "2026-09-03T20:00:00-07:00", "2026-09-03T23:00:00-07:00", false},
		{"ongoing_supported_end", "Aurora telescope viewing is available daily from 8pm to 11pm.", "2026-09-03T20:00:00-07:00", "2026-09-03T23:00:00-07:00", true},
		{"future_recurring_no_end", "Aurora telescope viewing is available every Thursday from 11pm.", "2026-09-03T23:00:00-07:00", "", false},
		{"future_recurring_supported_end", "Aurora telescope viewing is available every Thursday from 11pm to 11:45pm.", "2026-09-03T23:00:00-07:00", "2026-09-03T23:45:00-07:00", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			claim := webEvidenceClaim{Text: "Aurora telescope viewing is available.", SourceURL: evidenceFixtureURL,
				SupportQuote: "Aurora telescope viewing is available", ScheduleQuote: scenario.quote, StartsAt: scenario.start, EndsAt: scenario.end}
			reason := validateEvidenceClaim(claim, evidenceFixtureSources(scenario.quote), "What can I do tonight?", evidenceFixtureNow())
			if (reason == "") != scenario.wantValid {
				t.Errorf("valid=%t reason=%q, want valid=%t", reason == "", reason, scenario.wantValid)
			}
		})
	}
}
