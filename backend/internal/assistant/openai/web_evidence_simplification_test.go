package openai

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// These are separate, fully supported menu facts. The former generic preface
// alone pushed the two-result answer over the display limit and hid a venue.
func TestEvidenceSimplificationCompactIntroductionPreservesTwoOptions(t *testing.T) {
	t.Parallel()
	answer, sources := evidenceSimplificationMenuOptions()
	answer.Limitations = []string{"prices_unverified"}
	raw, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	response, rejected := renderWebEvidence(string(raw), sources, "Find two vegetarian dinner options and their menu prices.", evidenceFixtureNow())
	if len(rejected) != 0 || utf8.RuneCountInString(response) > 420 {
		t.Fatalf("supported options did not fit: response=%q rejected=%v", response, rejected)
	}
	for _, claim := range answer.Claims {
		if !strings.Contains(response, claim.Text) {
			t.Errorf("generic presentation displaced a supported option: %q", response)
		}
	}
	if !strings.HasPrefix(response, "Sources:\n1. ") || !strings.Contains(response, "\n2. ") ||
		!strings.Contains(response, "Current all-in prices unverified.") ||
		!strings.Contains(response, "(juniper.example)") || !strings.Contains(response, "(cedar.example)") {
		t.Errorf("compact rendering lost list, sources or price qualification: %q", response)
	}
}

func TestEvidenceSimplificationBudgetOmissionRequestsExistingRepair(t *testing.T) {
	t.Parallel()
	answer, sources := evidenceSimplificationMenuOptions()
	third := webEvidenceClaim{
		Text:      "Cypress Cafe lists a vegetarian bean stew with roasted potatoes for $16. This item is on its evening menu, and the listed price is per portion.",
		SourceURL: "https://cypress.example/menu",
	}
	third.SupportQuote = third.Text
	answer.Claims = append(answer.Claims, third)
	sources[third.SourceURL] = []webEvidenceSource{{URL: third.SourceURL, PageExcerpts: []string{third.SupportQuote}, ExtractionStatus: "succeeded"}}
	raw, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	response, rejected := renderWebEvidence(string(raw), sources, "Find three vegetarian dinner options and their menu prices.", evidenceFixtureNow())
	if len(rejected) != 1 || !strings.Contains(rejected[0], "response budget") {
		t.Fatalf("coverage loss did not trigger the agent's existing bounded repair: %v", rejected)
	}
	if utf8.RuneCountInString(response) > 420 || !strings.Contains(response, "Other details omitted.") || strings.Contains(response, third.Text) {
		t.Fatalf("unrepaired answer concealed omission or exceeded display bound: %q", response)
	}
	for _, claim := range answer.Claims[:2] {
		if !strings.Contains(response, claim.Text) {
			t.Errorf("whole supported claim was lost or truncated: %q", response)
		}
	}
}

func TestEvidenceSimplificationDuplicateClaimsDoNotDisplaceAnotherOption(t *testing.T) {
	t.Parallel()
	answer, sources := evidenceSimplificationMenuOptions()
	answer.Claims = []webEvidenceClaim{answer.Claims[0], answer.Claims[0], answer.Claims[1]}
	raw, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	response, rejected := renderWebEvidence(string(raw), sources, "Find two vegetarian dinner options and their menu prices.", evidenceFixtureNow())
	if len(rejected) != 0 || strings.Count(response, answer.Claims[0].Text) != 1 || !strings.Contains(response, answer.Claims[2].Text) || utf8.RuneCountInString(response) > 420 {
		t.Fatalf("duplicate claim displaced another supported result: response=%q rejected=%v", response, rejected)
	}
}

func evidenceSimplificationMenuOptions() (webEvidenceAnswer, map[string][]webEvidenceSource) {
	answer := webEvidenceAnswer{Claims: []webEvidenceClaim{
		{Text: "Juniper Cafe lists a vegetarian lentil bowl with roasted vegetables and rice for $18. This item is on its evening menu, and the listed price is per portion.", SourceURL: "https://juniper.example/menu"},
		{Text: "Cedar Cafe lists a vegetarian mushroom pasta with tomato sauce and seasonal greens for $17. This item is on its evening menu, and the listed price is per portion.", SourceURL: "https://cedar.example/menu"},
	}}
	sources := make(map[string][]webEvidenceSource)
	for index := range answer.Claims {
		claim := &answer.Claims[index]
		claim.SupportQuote = claim.Text
		sources[claim.SourceURL] = []webEvidenceSource{{URL: claim.SourceURL, PageExcerpts: []string{claim.SupportQuote}, ExtractionStatus: "succeeded"}}
	}
	return answer, sources
}
