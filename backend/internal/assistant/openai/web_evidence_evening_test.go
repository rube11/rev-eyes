package openai

import (
	"testing"
	"time"
)

func TestWebEvidenceCurrentNightRejectsUndatedShortShowtimeForEveningSynonym(t *testing.T) {
	t.Parallel()
	const excerpt = "Section: Aurora Observatory > Aurora Observatory lecture\n8:30pm"
	claim := webEvidenceClaim{
		Text: "Aurora Observatory lecture at 8:30pm.", SourceURL: evidenceFixtureURL,
		SupportQuote: "Aurora Observatory lecture", ScheduleQuote: excerpt,
		StartsAt: "2026-09-03T20:30:00-07:00",
	}
	// At 6pm this is a future start. Reject because its DATE is unsupported,
	// not because the performance has already started or lacks an end time.
	now := evidenceFixtureNow().Add(-4 * time.Hour)
	for _, query := range []string{"What can I do tonight?", "What can I do this evening?", "Any events THIS EVENING?", "What can I do this \t evening?"} {
		t.Run(query, func(t *testing.T) {
			reason := validateEvidenceClaim(claim, evidenceFixtureSources(excerpt), query, now)
			if reason != "a start-only performance needs its explicit event date" {
				t.Errorf("undated short showtime must fail the explicit date check: reason=%q", reason)
			}
		})
	}
}

func TestWebEvidenceCurrentNightAcceptsExplicitFutureDateWithoutInventedEnd(t *testing.T) {
	t.Parallel()
	const quote = "Aurora Observatory lecture on September 3, 2026 at 8:30pm."
	claim := webEvidenceClaim{
		Text: "Aurora Observatory lecture at 8:30pm.", SourceURL: evidenceFixtureURL,
		SupportQuote: "Aurora Observatory lecture", ScheduleQuote: quote,
		StartsAt: "2026-09-03T20:30:00-07:00",
	}
	now := evidenceFixtureNow().Add(-4 * time.Hour)
	for _, query := range []string{"What can I do tonight?", "What can I do this evening?", "What can I do THIS\nEVENING?"} {
		t.Run(query, func(t *testing.T) {
			if reason := validateEvidenceClaim(claim, evidenceFixtureSources(quote), query, now); reason != "" {
				t.Errorf("explicit future date/start should pass without invented ending: %s", reason)
			}
		})
	}
}

func TestWebEvidenceCurrentNightGuardDoesNotApplyToUnrelatedQueries(t *testing.T) {
	t.Parallel()
	const quote = "Aurora Observatory offers telescope lectures."
	claim := webEvidenceClaim{Text: quote, SourceURL: evidenceFixtureURL, SupportQuote: quote}
	for _, query := range []string{
		"What programs does Aurora Observatory offer?",
		"What can I do tomorrow evening?",
		"Find the Tonightly program archive.",
		"What is the NotTonight exhibit about?",
		"Which programs have evening in their title?",
		"What does this eveningside exhibit show?",
	} {
		t.Run(query, func(t *testing.T) {
			if reason := validateEvidenceClaim(claim, evidenceFixtureSources(quote), query, evidenceFixtureNow()); reason != "" {
				t.Errorf("unrelated query acquired a current-night interval requirement: %s", reason)
			}
			if got := webEvidenceAbstention(query); got != "I couldn't verify the requested details from the available sources." {
				t.Errorf("unrelated query acquired a current-night abstention: %q", got)
			}
		})
	}
}

func TestWebEvidenceCurrentNightAbstentionIncludesEveningSynonym(t *testing.T) {
	t.Parallel()
	for _, query := range []string{"What can I do tonight?", "What can I do this evening?", "Any options THIS\tEVENING?"} {
		t.Run(query, func(t *testing.T) {
			if got := webEvidenceAbstention(query); got != "I couldn't verify a remaining-night option with the available sources." {
				t.Errorf("current-night synonym did not use its scoped abstention: %q", got)
			}
		})
	}
}
