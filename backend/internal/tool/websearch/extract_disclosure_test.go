package websearch

import (
	"strings"
	"testing"
)

func TestDisclosureFeesKeepTheRequestedPlaceOwner(t *testing.T) {
	markup := `<main><h1>Fees</h1><p>Fees for all regional parks.</p>
<div class="accordion__tab"><input type="checkbox" id="north">
<label for="north" class="accordion__tab-label">North Ridge State Park</label>
<div><p>Day use entrance fee: $5 per vehicle (Non-resident vehicles $10 per vehicle)</p></div></div>
<div class="accordion__tab"><input type="checkbox" id="cedar">
<label for="cedar" class="accordion__tab-label">Cedar Mesa State Park</label>
<div><p>Day use entrance fee: $10 per vehicle (Non-resident vehicles $15 per vehicle)</p></div></div>
<div class="accordion__tab"><input type="checkbox" id="south">
<label for="south" class="accordion__tab-label">South Valley State Park</label>
<div><p>Day use entrance fee: $8 per vehicle (Non-resident vehicles $12 per vehicle)</p></div></div></main>`
	text := extractReadableText(markup)
	selected := selectQueryChunks("Cedar Mesa State Park entrance fee non-resident car", text)
	if !strings.Contains(selected, "Section: Fees > Cedar Mesa State Park\nDay use entrance fee: $10 per vehicle (Non-resident vehicles $15 per vehicle)") {
		t.Fatalf("requested fee lost its accordion owner: %q", selected)
	}
	for _, block := range strings.Split(selected, "\n\n") {
		if strings.Contains(block, "Non-resident vehicles $10") && !strings.Contains(block, "North Ridge") ||
			strings.Contains(block, "Non-resident vehicles $12") && !strings.Contains(block, "South Valley") {
			t.Errorf("another place's fee became unowned: %q", block)
		}
	}
}

func TestDisclosureLabelsExcludeOrdinaryFormControls(t *testing.T) {
	markup := `<main><h1>Visitor guide</h1><label for="email">Email address</label>
<label class="accordion__tab-label">Unbound label</label>
<label for="consent" class="checkbox-label">Accept marketing</label>
<p>Cedar Mesa visitor information.</p></main>`
	text := extractReadableText(markup)
	if !strings.Contains(text, "Cedar Mesa visitor information.") {
		t.Fatal("ordinary-label assertion lost the surrounding source text")
	}
	if strings.Contains(text, disclosureStartMarker) || strings.Contains(text, disclosureEndMarker) {
		t.Fatalf("ordinary form controls became disclosure boundaries: %q", text)
	}
	selected := selectQueryChunks("Cedar Mesa visitor information", text)
	if !strings.Contains(selected, "Section: Visitor guide\nCedar Mesa visitor information.") {
		t.Fatalf("ordinary labels changed the following paragraph's owner: %q", selected)
	}
	for _, unwanted := range []string{"Email address", "Unbound label", "Accept marketing"} {
		if strings.Contains(text, "[Heading 2] "+unwanted) {
			t.Errorf("ordinary form label became a section heading: %q", text)
		}
	}
}

func TestFeeQueryPrefersConcreteAmountToRepeatedMarketing(t *testing.T) {
	text := "[Heading 1] Cedar Mesa\n" +
		"Cedar Mesa State Park welcomes visitors to its historic landscape.\n" +
		"Cedar Mesa State Park offers many trails for curious visitors.\n" +
		"Cedar Mesa State Park has a rich history and unusual geology.\n" +
		"[Heading 2] Entrance fee\n$15 per non-resident vehicle."
	for _, query := range []string{"Cedar Mesa entrance fee", "Cedar Mesa fees"} {
		if got := selectQueryChunks(query, text); !strings.Contains(got, "$15 per non-resident vehicle.") {
			t.Errorf("concrete fee displaced by repetitive marketing for %q: %q", query, got)
		}
	}
}
