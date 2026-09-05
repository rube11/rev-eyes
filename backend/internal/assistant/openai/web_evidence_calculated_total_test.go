package openai

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// These fixtures verify a narrowly labeled arithmetic exception. They do not
// establish serving size, nutritional adequacy, or whether an item is a meal.
func TestCalculatedTotalAllowsCentsExactExplicitPartySubtotal(t *testing.T) {
	const quote = "Lentil stew costs $17.25 per portion."
	for _, test := range []struct{ name, text, query, quote string }{
		{"word_quantity_about", "Lentil stew is $17.25 per portion; three people ordering one portion each would pay about $51.75 before tax and tip.", "Find a dinner option for three people.", quote},
		{"digit_quantity_estimated", "For 3 people, lentil stew has an estimated $51.75 before tax and tip.", "Find a dinner option for 3 people.", quote},
		{"exact_subtotal_without_estimate_keyword", "Three people ordering lentil stew would pay $51.75 before tax and tip.", "Find a dinner option for three people.", quote},
		{"repeated_same_unit_price", "Three people ordering lentil stew would pay about $51.75 before tax and tip.", "Find a dinner option for three people.", quote + " The listed portion price is $17.25."},
	} {
		t.Run(test.name, func(t *testing.T) {
			if claimNumbersSupported(test.text, test.quote, evidenceFixtureNow().Location()) {
				t.Fatal("fixture must require calculation rather than passing the old literal guard")
			}
			if !claimNumbersSupportedForQuery(test.text, test.quote, test.query, evidenceFixtureNow().Location()) {
				t.Fatalf("explicit cents-exact subtotal was rejected: %q", test.text)
			}
		})
	}
}

func TestCalculatedTotalRejectsUnsupportedOrAmbiguousCalculations(t *testing.T) {
	const quote = "Lentil stew costs $17.25 per portion."
	const text = "Three people ordering lentil stew would pay about $51.75 before tax and tip."
	const query = "Find a dinner option for three people."
	for _, test := range []struct{ name, text, query, quote string }{
		{"wrong_sum", strings.Replace(text, "$51.75", "$51.76", 1), query, quote},
		{"negative_subtotal", strings.Replace(text, "about $51.75", "-$51.75", 1), query, quote},
		{"negative_subtotal_spaced", strings.Replace(text, "about $51.75", "− $51.75", 1), query, quote},
		{"signed_adjustment", strings.Replace(text, "about $51.75", "+$51.75", 1), query, quote},
		{"rounded_not_cents_exact", strings.Replace(text, "$51.75", "$52", 1), query, quote},
		{"claim_party_mismatch", strings.Replace(text, "Three people", "Four people", 1), query, quote},
		{"query_party_mismatch", text, "Find a dinner option for two people.", quote},
		{"missing_claim_party", "Lentil stew would cost about $51.75 before tax and tip.", query, quote},
		{"missing_query_party", text, "Find a dinner option.", quote},
		{"ambiguous_query_party", text, "Find a dinner option for two or three people.", quote},
		{"ambiguous_query_digit_party", text, "Find a dinner option for 2 or 3 people.", quote},
		{"query_party_range", text, "Find a dinner option for 2-3 people.", quote},
		{"query_mixed_age_groups", text, "Find a dinner option for two adults and three children.", quote},
		{"query_decimal_party", text, "Find a dinner option for 2.3 people.", quote},
		{"query_negative_party", text, "Find a dinner option for -3 people.", quote},
		{"query_up_to_party", text, "Find a dinner option for up to three people.", quote},
		{"query_at_least_party", text, "Find a dinner option for at least three people.", quote},
		{"query_approximate_party", text, "Find a dinner option for about three people.", quote},
		{"two_query_groups", text, "Find a dinner option for three people, or for four people if our friend joins.", quote},
		{"ambiguous_claim_party", "Two or three people would pay about $51.75 before tax and tip.", query, quote},
		{"negative_claim_party", "-3 people would pay about $51.75 before tax and tip.", query, quote},
		{"approximate_claim_party", "Up to three people would pay about $51.75 before tax and tip.", query, quote},
		{"missing_tax_tip_exclusion", strings.Replace(text, " before tax and tip", "", 1), query, quote},
		{"tax_tip_included", strings.Replace(text, "before tax and tip", "including tax and tip", 1), query, quote},
		{"later_all_in_contradiction", text + " That includes tax and tip.", query, quote},
		{"all_in_claim", "Three people can pay about $51.75 all-in before tax and tip.", query, quote},
		{"missing_source_currency", text, query, "Lentil stew costs 17.25 per portion."},
		{"different_source_currency", text, query, "Lentil stew costs €17.25 per portion."},
		{"source_canadian_dollars", text, query, "Lentil stew costs CAD $17.25 per portion."},
		{"source_australian_dollars", text, query, "Lentil stew costs AUD $17.25 per portion."},
		{"source_mixed_dollar_currencies", text, query, "Lentil stew costs US $17.25 or CAD $17.25 per portion."},
		{"claim_foreign_dollar_code", strings.Replace(text, "$51.75", "CAD $51.75", 1), query, quote},
		{"different_claim_currency", strings.Replace(text, "$51.75", "€51.75", 1), query, quote},
		{"missing_claim_currency", strings.Replace(text, "$51.75", "51.75", 1), query, quote},
		{"multiple_unit_prices", text, query, quote + " Mushroom broth costs $12.50 per portion."},
		{"unit_price_range", text, query, "Lentil stew costs $17.25–$19.25 per portion."},
		{"single_symbol_price_range", text, query, "Lentil stew costs $17.25–19.25 per portion."},
		{"textual_price_range", text, query, "Lentil stew costs $17.25 to $19.25 per portion."},
		{"source_price_plus", text, query, "Lentil stew costs $17.25+ per portion."},
		{"source_price_and_up", text, query, "Lentil stew costs $17.25 and up per portion."},
		{"source_scientific_notation", text, query, "Lentil stew costs $17.25e2 per portion."},
		{"query_budget_not_source_fact", text + " That is under $60.", "Find a dinner option for three people under $60.", quote},
		{"derived_total_not_source_price_elsewhere", text + " The portion itself also costs $51.75.", query, quote},
		{"unexplained_extra_number", text + " It supplies 900 calories.", query, quote},
	} {
		t.Run(test.name, func(t *testing.T) {
			if claimNumbersSupportedForQuery(test.text, test.quote, test.query, evidenceFixtureNow().Location()) {
				t.Fatalf("unsupported calculation passed: text=%q query=%q quote=%q", test.text, test.query, test.quote)
			}
		})
	}
}

func TestCalculatedTotalPreservesLiteralNumberContract(t *testing.T) {
	for _, test := range []struct{ text, quote string }{
		{"Lentil stew costs $17.25.", "Lentil stew costs $17.25 per portion."},
		{"The hall opens at 8pm.", "The hall opens daily at 8pm."},
		{"Admission costs $0.", "Admission is free."},
	} {
		if !claimNumbersSupported(test.text, test.quote, evidenceFixtureNow().Location()) || !claimNumbersSupportedForQuery(test.text, test.quote, "Find current details.", evidenceFixtureNow().Location()) {
			t.Fatalf("literal support changed: text=%q quote=%q", test.text, test.quote)
		}
	}
}

func TestCalculatedTotalRendererKeepsEstimateCitationAndPartialLimit(t *testing.T) {
	const endpoint = "https://juniper.example/menu"
	const quote = "Lentil stew costs $17.25 per portion."
	const claimText = "Three people ordering lentil stew would pay about $51.75 before tax and tip."
	claim := webEvidenceClaim{Text: claimText, SourceURL: endpoint, SupportQuote: quote}
	answer := webEvidenceAnswer{Claims: []webEvidenceClaim{claim}, Limitations: []string{"prices_unverified", "partial"}}
	sources := map[string][]webEvidenceSource{endpoint: {{URL: endpoint, PageExcerpts: []string{"Section: Juniper Kitchen > Dinner\n" + quote}, ExtractionStatus: "succeeded"}}}
	raw, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	response, rejected := renderWebEvidence(string(raw), sources, "Find a dinner option for three people.", evidenceFixtureNow())
	if len(rejected) != 0 || !strings.Contains(response, claimText+" (juniper.example)") || !strings.Contains(response, "Current all-in prices unverified.") || !strings.Contains(response, "Only partially verified.") || utf8.RuneCountInString(response) > 420 {
		t.Fatalf("accepted arithmetic lost its estimate, citation, caveats, or response limit: response=%q rejected=%v", response, rejected)
	}
	if strings.Contains(response, "“") || strings.Contains(response, "Other details unverified.") {
		t.Fatalf("valid estimate was incorrectly converted into a quoted rescue: %q", response)
	}

	// Repeating complete valid claims must still drop whole claims to stay within
	// the display contract; it cannot trim away the tax/tip qualification.
	longText := "Three people each ordering Juniper Kitchen's handmade slow-simmered lentil stew with seasonal vegetables would pay about $51.75 before tax and tip."
	claim.Text = longText
	answer.Claims = []webEvidenceClaim{claim, claim, claim}
	raw, err = json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	response, rejected = renderWebEvidence(string(raw), sources, "Find a dinner option for three people.", evidenceFixtureNow())
	if len(rejected) != 0 || utf8.RuneCountInString(response) > 420 || !strings.Contains(response, longText+" (juniper.example)") || !strings.Contains(response, "Current all-in prices unverified.") {
		t.Fatalf("bounded multi-claim arithmetic output failed: response=%q rejected=%v", response, rejected)
	}
	if strings.Count(response, "Three people") != strings.Count(response, longText) || strings.Count(response, longText) >= 3 {
		t.Fatalf("estimate was truncated instead of dropped whole: %q", response)
	}
}

func TestCalculatedTotalCannotCreateSourceQuoteOrChangeItsURL(t *testing.T) {
	const endpoint = "https://juniper.example/menu"
	const quote = "Lentil stew costs $17.25 per portion."
	const claimText = "Three people ordering lentil stew would pay about $51.75 before tax and tip."
	sources := map[string][]webEvidenceSource{endpoint: {{URL: endpoint, PageExcerpts: []string{"Section: Juniper Kitchen > Dinner\n" + quote}, ExtractionStatus: "succeeded"}}}
	for _, claim := range []webEvidenceClaim{
		{Text: claimText, SourceURL: endpoint, SupportQuote: quote + " Three portions cost $51.75."},
		{Text: claimText, SourceURL: endpoint + "?other", SupportQuote: quote},
	} {
		raw, err := json.Marshal(webEvidenceAnswer{Claims: []webEvidenceClaim{claim}})
		if err != nil {
			t.Fatal(err)
		}
		query := "Find a dinner option for three people."
		response, rejected := renderWebEvidence(string(raw), sources, query, evidenceFixtureNow())
		if response != webEvidenceAbstention(query) || len(rejected) == 0 {
			t.Fatalf("arithmetic bypassed source provenance: response=%q rejected=%v", response, rejected)
		}
	}
}

func TestCalculatedTotalCannotBorrowUnitPriceFromOtherQuoteFields(t *testing.T) {
	const endpoint = "https://juniper.example/menu"
	const support = "Lentil stew is served at dinner."
	const otherItem = "Mushroom broth costs $17.25 per portion."
	sources := map[string][]webEvidenceSource{endpoint: {{URL: endpoint, PageExcerpts: []string{
		"Section: Juniper Kitchen > Lentil stew\n" + support,
		"Section: Juniper Kitchen > Mushroom broth\n" + otherItem,
	}, ExtractionStatus: "succeeded"}}}
	for _, test := range []struct{ name, dateQuote, scheduleQuote string }{
		{"date_quote", otherItem, ""},
		{"schedule_quote", "", otherItem},
	} {
		t.Run(test.name, func(t *testing.T) {
			claim := webEvidenceClaim{
				Text:      "Three people ordering lentil stew would pay about $51.75 before tax and tip.",
				SourceURL: endpoint, SupportQuote: support, DateQuote: test.dateQuote, ScheduleQuote: test.scheduleQuote,
			}
			if reason := validateEvidenceClaim(claim, sources, "Find a dinner option for three people.", evidenceFixtureNow()); reason == "" {
				t.Fatalf("calculation borrowed another item's price through %s", test.name)
			}
		})
	}
}
