package openai

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

const usefulPartialURL = "https://lumen.example/research"

func TestUsefulPartialRescuesWholeFetchedBlockNotUnsupportedComparison(t *testing.T) {
	const block = "Section: Lumen Grove > Park grounds\nThe park grounds open daily at 6am."
	claim := webEvidenceClaim{Text: "Yes, Lumen Grove opens before 8am.", SourceURL: usefulPartialURL, SupportQuote: "The park grounds open daily at 6am."}
	response, rejected := renderUsefulPartial(t, "Does Lumen Grove open before 8am?", claim, []string{block})
	assertUsefulPartialQuote(t, response, rejected, block)
	if strings.Contains(response, "8am") || strings.Contains(strings.ToLower(response), "yes,") || strings.Contains(response, claim.Text) {
		t.Fatalf("rescue repeated an unsupported threshold comparison: %q", response)
	}
}

func TestUsefulPartialKeepsActualExplanationAndDropsInventedScientificNumbers(t *testing.T) {
	const block = "Section: Prism Laboratory > Light scattering\nShorter wavelengths scatter more strongly than longer wavelengths in clean air."
	claim := webEvidenceClaim{Text: "Prism Laboratory explains a 128-fold scattering increase for particles 2 microns wide.", SourceURL: usefulPartialURL, SupportQuote: "Shorter wavelengths scatter more strongly than longer wavelengths in clean air."}
	response, rejected := renderUsefulPartial(t, "Explain why light scatters differently by wavelength.", claim, []string{block})
	assertUsefulPartialQuote(t, response, rejected, block)
	if strings.Contains(response, "128") || strings.Contains(response, "2 microns") || strings.Contains(response, "fold") {
		t.Fatalf("rescue retained fabricated quantitative detail: %q", response)
	}
}

func TestUsefulPartialQuotesPriceWithoutComputingAnAllInTotal(t *testing.T) {
	const block = "Section: Lumen Bistro > Dinner\nLentil stew costs $17 per portion; drinks and service are extra."
	claim := webEvidenceClaim{Text: "Two dinners with drinks cost $34 total.", SourceURL: usefulPartialURL, SupportQuote: "Lentil stew costs $17 per portion; drinks and service are extra."}
	response, rejected := renderUsefulPartial(t, "What is the all-in price for two dinners with drinks?", claim, []string{block})
	assertUsefulPartialQuote(t, response, rejected, block)
	if strings.Contains(response, "$34") || strings.Contains(response, "Two dinners") || !strings.Contains(response, "drinks and service are extra") {
		t.Fatalf("quoted menu facts became unsupported price arithmetic or lost exclusions: %q", response)
	}
}

func TestUsefulPartialKeepsWholeBlockNegationAndOwningSection(t *testing.T) {
	const block = "Section: Lumen Grove > Visitor center\nThe visitor center is not open at 6am; it opens at 7am."
	claim := webEvidenceClaim{Text: "The visitor center opens before 8am.", SourceURL: usefulPartialURL, SupportQuote: "open at 6am"}
	response, rejected := renderUsefulPartial(t, "When does Lumen Grove's visitor center open?", claim, []string{block})
	assertUsefulPartialQuote(t, response, rejected, block)
	if !strings.Contains(response, "not open at 6am") || !strings.Contains(response, "Visitor center") || strings.Contains(response, "8am") {
		t.Fatalf("rescue removed negative context or section ownership: %q", response)
	}
}

func TestUsefulPartialCannotTrimOwnershipOrQualifiersToFit(t *testing.T) {
	const short = "The park grounds open daily at 6am."
	block := "Section: Lumen Grove > Restricted maintenance entrance\n" + short + " " + strings.Repeat("Visitors require an escort. ", 10)
	claim := webEvidenceClaim{Text: "The park opens before 8am.", SourceURL: usefulPartialURL, SupportQuote: short}
	query := "Does Lumen Grove open before 8am?"
	response, rejected := renderUsefulPartial(t, query, claim, []string{block})
	if response != webEvidenceAbstention(query) || len(rejected) == 0 {
		t.Fatalf("overlong owned block was reduced to a misleading short substring: response=%q rejected=%v", response, rejected)
	}
}

func TestUsefulPartialRejectsOwnerlessClockInsteadOfInferringParkOwnership(t *testing.T) {
	const block = "Daily hours: 6am to 10pm."
	claim := webEvidenceClaim{Text: "Lumen Grove park grounds open before 8am.", SourceURL: usefulPartialURL, SupportQuote: block}
	query := "Do Lumen Grove park grounds open before 8am?"
	response, rejected := renderUsefulPartial(t, query, claim, []string{block})
	if response != webEvidenceAbstention(query) || len(rejected) == 0 {
		t.Fatalf("ownerless hours were rescued without establishing which facility they describe: response=%q rejected=%v", response, rejected)
	}
}

func TestUsefulPartialStillRejectsDiscoveryForgedWrongURLAndSplicedQuotes(t *testing.T) {
	const block = "Section: Lumen Grove > Park grounds\nThe park grounds open daily at 6am."
	const body = "The park grounds open daily at 6am."
	query := "Does Lumen Grove open before 8am?"
	base := webEvidenceClaim{Text: "Lumen Grove opens before 8am.", SourceURL: usefulPartialURL, SupportQuote: body}
	for _, test := range []struct {
		name  string
		claim webEvidenceClaim
		rows  []webEvidenceSource
	}{
		{"discovery_only", base, []webEvidenceSource{{URL: usefulPartialURL, DiscoverySnippet: block, Snippet: block, ExtractionStatus: "unavailable"}}},
		{"invented_quote", webEvidenceClaim{Text: base.Text, SourceURL: usefulPartialURL, SupportQuote: "The park grounds open daily at 5am."}, []webEvidenceSource{{URL: usefulPartialURL, PageExcerpts: []string{block}, ExtractionStatus: "succeeded"}}},
		{"wrong_exact_url", webEvidenceClaim{Text: base.Text, SourceURL: usefulPartialURL + "?other", SupportQuote: body}, []webEvidenceSource{{URL: usefulPartialURL, PageExcerpts: []string{block}, ExtractionStatus: "succeeded"}}},
		{"spliced_blocks", webEvidenceClaim{Text: base.Text, SourceURL: usefulPartialURL, SupportQuote: body + " Visitors must use the west gate."}, []webEvidenceSource{{URL: usefulPartialURL, PageExcerpts: []string{block, "Visitors must use the west gate."}, ExtractionStatus: "succeeded"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, _ := json.Marshal(webEvidenceAnswer{Claims: []webEvidenceClaim{test.claim}})
			response, rejected := renderWebEvidence(string(raw), map[string][]webEvidenceSource{usefulPartialURL: test.rows}, query, evidenceFixtureNow())
			if response != webEvidenceAbstention(query) || len(rejected) == 0 {
				t.Fatalf("invalid rescue became an answer: response=%q rejected=%v", response, rejected)
			}
		})
	}
}

func TestUsefulPartialNumericFailureCannotMaskExpiredOrExcludedSchedule(t *testing.T) {
	for _, test := range []struct{ name, block, end string }{
		{"expired", "Section: Lumen Hall > Performances\nLumen Hall performs daily from 6pm to 9pm.", "2026-09-03T21:00:00-07:00"},
		{"cancelled", "Section: Lumen Hall > Performances\nLumen Hall performs daily from 6pm to 11pm. Tonight is cancelled.", "2026-09-03T23:00:00-07:00"},
	} {
		t.Run(test.name, func(t *testing.T) {
			claim := webEvidenceClaim{Text: "Lumen Hall has 3 performances available tonight.", SourceURL: usefulPartialURL, SupportQuote: test.block, ScheduleQuote: test.block, StartsAt: "2026-09-03T18:00:00-07:00", EndsAt: test.end}
			query := "What can I do tonight?"
			response, rejected := renderUsefulPartial(t, query, claim, []string{test.block})
			if response != webEvidenceAbstention(query) || len(rejected) == 0 || strings.Contains(strings.Join(rejected, " "), "a number in the claim is absent") {
				t.Fatalf("numeric rejection masked an invalid schedule: response=%q rejected=%v", response, rejected)
			}
		})
	}
}

func TestUsefulPartialNumericFailureCannotMaskSeasonContradiction(t *testing.T) {
	const block = "Lumen Grove reservations are required October 1 to May 31."
	claim := webEvidenceClaim{Text: "Yes, 2 reservations are needed tomorrow.", SourceURL: usefulPartialURL, SupportQuote: block}
	query := "Do I need a reservation tomorrow?"
	response, rejected := renderUsefulPartial(t, query, claim, []string{block})
	if response != webEvidenceAbstention(query) || len(rejected) == 0 || strings.Contains(strings.Join(rejected, " "), "a number in the claim is absent") {
		t.Fatalf("numeric rejection hid the original claim's wrong reservation season: response=%q rejected=%v", response, rejected)
	}
}

func TestUsefulPartialOutputKeepsWholeQuotesAndCaveatWithin420Runes(t *testing.T) {
	answer := webEvidenceAnswer{}
	sources := map[string][]webEvidenceSource{}
	var blocks []string
	for index := 0; index < 3; index++ {
		name := []string{"Lumen", "Prism", "Solace"}[index]
		block := "Section: " + name + " Laboratory > Observing\n" + name + " offers guided viewing. " + strings.Repeat("Clear-sky observing is required. ", 4)
		blocks = append(blocks, block)
		endpoint := fmt.Sprintf("https://%s.example/research", strings.ToLower(name))
		sources[endpoint] = []webEvidenceSource{{URL: endpoint, PageExcerpts: []string{block}, ExtractionStatus: "succeeded"}}
		answer.Claims = append(answer.Claims, webEvidenceClaim{Text: name + " offers 900 guided sessions.", SourceURL: endpoint, SupportQuote: name + " offers guided viewing."})
	}
	raw, _ := json.Marshal(answer)
	response, rejected := renderWebEvidence(string(raw), sources, "Find guided observing options.", evidenceFixtureNow())
	if response == webEvidenceAbstention("Find guided observing options.") || utf8.RuneCountInString(response) > 420 || !strings.Contains(response, "Other details unverified.") || len(rejected) != 3 {
		t.Fatalf("useful bounded quotation/caveat contract failed: response=%q rejected=%v", response, rejected)
	}
	for index, block := range blocks {
		name := []string{"Lumen", "Prism", "Solace"}[index]
		if strings.Contains(response, name) && !strings.Contains(response, "“"+strings.Join(strings.Fields(block), " ")+"”") {
			t.Fatalf("renderer truncated a rescued quote instead of dropping it whole: %q", response)
		}
	}
}

func renderUsefulPartial(t *testing.T, query string, claim webEvidenceClaim, blocks []string) (string, []string) {
	t.Helper()
	raw, err := json.Marshal(webEvidenceAnswer{Claims: []webEvidenceClaim{claim}})
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string][]webEvidenceSource{usefulPartialURL: {{URL: usefulPartialURL, PageExcerpts: blocks, ExtractionStatus: "succeeded"}}}
	return renderWebEvidence(string(raw), sources, query, evidenceFixtureNow())
}

func assertUsefulPartialQuote(t *testing.T, response string, rejected []string, block string) {
	t.Helper()
	quote := "“" + strings.Join(strings.Fields(block), " ") + "” (lumen.example)"
	if !strings.Contains(response, quote) || !strings.Contains(response, "Other details unverified.") || len(rejected) != 1 || !strings.Contains(rejected[0], "a number in the claim is absent") || utf8.RuneCountInString(response) > 420 {
		t.Fatalf("rescue must retain exact whole source, attribution, original rejection, and uncertainty: response=%q rejected=%v", response, rejected)
	}
}
