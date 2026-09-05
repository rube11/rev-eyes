package openai

import (
	"strings"
	"testing"
)

func TestIndependentExactSubtotalReferenceRendererPreservesNarrowArithmeticScope(t *testing.T) {
	const endpoint = "https://harbor.example/dinner"
	const query = "Find a dinner option for three people."
	const passage = "Section: Harbor Kitchen > Dinner\nLentil stew costs $17.25 per portion."
	const exact = "Three people ordering one portion of lentil stew each would pay $51.75 before tax and tip."
	for _, test := range []struct {
		name, claim, source string
		accepted            bool
	}{
		{"exact_without_estimate_keyword", exact, passage, true},
		{"wrong_cents", strings.Replace(exact, "$51.75", "$51.76", 1), passage, false},
		{"negative_subtotal", strings.Replace(exact, "$51.75", "-$51.75", 1), passage, false},
		{"unicode_negative_subtotal", strings.Replace(exact, "$51.75", "− $51.75", 1), passage, false},
		{"positive_signed_adjustment", strings.Replace(exact, "$51.75", "+ $51.75", 1), passage, false},
		{"all_in_contradiction", exact + " That includes tax and tip.", passage, false},
		{"additional_source_price", exact, passage + " Mushroom broth costs $12.50 per portion.", false},
		{"reused_total_as_portion_price", exact + " The portion itself costs $51.75.", passage, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			refs := independentEvidenceReferences(t, map[string][]webEvidenceSource{
				endpoint: {{URL: endpoint, ExtractionStatus: "succeeded", PageExcerpts: []string{test.source}}},
			})
			id := independentEvidenceReferenceID(t, refs, endpoint, test.source)
			// Every negative case has a genuinely usable source and valid ID;
			// rejection must come from the calculation, not missing provenance.
			literal := independentReferencedAnswer(t, independentReferencedClaim(id, "Lentil stew costs $17.25 per portion."))
			literalResponse, literalRejected := renderReferencedWebEvidence(literal, refs, query, evidenceFixtureNow())
			if len(literalRejected) != 0 || !strings.Contains(literalResponse, "Lentil stew costs $17.25 per portion. (harbor.example)") {
				t.Fatalf("literal positive control failed: response=%q rejected=%v", literalResponse, literalRejected)
			}
			if claimNumbersSupported(test.claim, test.source, evidenceFixtureNow().Location()) {
				t.Fatal("fixture must require derived arithmetic rather than literal number membership")
			}
			raw := independentReferencedAnswer(t, independentReferencedClaim(id, test.claim))
			response, rejected := renderReferencedWebEvidence(raw, refs, query, evidenceFixtureNow())
			if test.accepted {
				if len(rejected) != 0 || response != test.claim+" (harbor.example)" {
					t.Fatalf("exact subtotal was rejected, rewritten, or rescued instead of accepted: response=%q rejected=%v", response, rejected)
				}
				return
			}
			if len(rejected) != 1 || !strings.Contains(rejected[0], "a number in the claim is absent") || strings.Contains(response, "$51.75") || strings.Contains(response, "$51.76") || strings.Contains(response, test.claim) {
				t.Fatalf("unsupported subtotal escaped its narrow guard: response=%q rejected=%v", response, rejected)
			}
		})
	}
}
