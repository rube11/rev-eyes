package openai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderWebEvidenceStripsNumericEmbellishmentOnlyForRevalidatedDatedAnnouncement(t *testing.T) {
	t.Parallel()
	const quote = "The Aurora club announced Monday that its new telescope opened."
	claim := webEvidenceClaim{Text: "The Aurora club opened 12 telescopes.", SourceURL: evidenceFixtureURL, SupportQuote: quote, DateQuote: quote, EventDate: "2026-08-31"}
	sources := map[string][]webEvidenceSource{evidenceFixtureURL: {{URL: evidenceFixtureURL, Snippet: quote, PublishedDate: "2026-08-31T18:00:00Z"}}}
	raw, _ := json.Marshal(webEvidenceAnswer{Claims: []webEvidenceClaim{claim}})
	response, rejected := renderWebEvidence(string(raw), sources, "What changed this week?", evidenceFixtureNow())
	if !strings.Contains(response, "“"+quote+"” (aurora.example)") || strings.Contains(response, "12") || !strings.Contains(response, "Other details unverified.") {
		t.Errorf("verbatim source salvage changed facts or omitted uncertainty: %q", response)
	}
	if len(rejected) != 1 || !strings.Contains(rejected[0], "a number in the claim is absent") {
		t.Errorf("original numeric embellishment rejection disappeared: %#v", rejected)
	}
}

func TestRenderWebEvidenceVerbatimSalvageStillRejectsStaleForgedOrLongQuotes(t *testing.T) {
	t.Parallel()
	const base = "The Aurora club announced Monday that its new telescope opened."
	for _, scenario := range []struct{ name, quote, snippet, published, eventDate string }{
		{"stale_event", base, base, "2026-08-24T18:00:00Z", "2026-08-24"},
		{"forged_quote", "The Aurora club announced Monday that a lunar observatory opened.", base, "2026-08-31T18:00:00Z", "2026-08-31"},
		{"overlong_quote", base + strings.Repeat(" Additional detail.", 20), base + strings.Repeat(" Additional detail.", 20), "2026-08-31T18:00:00Z", "2026-08-31"},
		{"multiline_quote", base + "\nMore unrelated information.", base + "\nMore unrelated information.", "2026-08-31T18:00:00Z", "2026-08-31"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			claim := webEvidenceClaim{Text: "The Aurora club opened 12 telescopes.", SourceURL: evidenceFixtureURL, SupportQuote: scenario.quote, DateQuote: scenario.quote, EventDate: scenario.eventDate}
			sources := map[string][]webEvidenceSource{evidenceFixtureURL: {{URL: evidenceFixtureURL, Snippet: scenario.snippet, PublishedDate: scenario.published}}}
			raw, _ := json.Marshal(webEvidenceAnswer{Claims: []webEvidenceClaim{claim}})
			response, rejected := renderWebEvidence(string(raw), sources, "What changed this week?", evidenceFixtureNow())
			if response != webEvidenceAbstention("What changed this week?") || len(rejected) == 0 {
				t.Errorf("invalid verbatim rescue accepted: response=%q rejected=%#v", response, rejected)
			}
		})
	}
}

func TestRenderWebEvidenceNoVerbatimNumericSalvageForGeneralPricesOrTonight(t *testing.T) {
	t.Parallel()
	const quote = "Aurora telescope admission costs $18 per adult."
	claim := webEvidenceClaim{Text: "Aurora telescope admission costs $12 per adult.", SourceURL: evidenceFixtureURL, SupportQuote: quote}
	raw, _ := json.Marshal(webEvidenceAnswer{Claims: []webEvidenceClaim{claim}})
	for _, query := range []string{"What do tickets cost?", "Tell me about the telescope.", "What can I do tonight?", "What do tickets cost today?"} {
		t.Run(query, func(t *testing.T) {
			response, rejected := renderWebEvidence(string(raw), evidenceFixtureSources(quote), query, evidenceFixtureNow())
			if response != webEvidenceAbstention(query) || len(rejected) == 0 {
				t.Errorf("out-of-scope numeric salvage accepted: response=%q rejected=%#v", response, rejected)
			}
		})
	}
}
