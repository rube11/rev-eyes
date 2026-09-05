package websearch

import (
	"encoding/json"
	"strings"
	"testing"
)

func independentMenuCard(name, price, other string) string {
	return `<section class="menu-item menu-item-full"><h3>` + name + `</h3><span class="item-price">` + price + `</span>` + other + `</section>`
}

func independentMenuCardRow(t *testing.T, text, name string) string {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, menuItemPrefix+name+";") {
			return line
		}
	}
	t.Fatalf("expected independently owned card %q, got %q", name, text)
	return ""
}

func TestIndependentMenuCardKeepsBasePriceCaloriesAndAddonsDistinct(t *testing.T) {
	markup := `<main><h1>Kitchen menu</h1>` +
		independentMenuCard("Tempeh Bowl", "11.00", `<span class="allergy-icon icon-allergy-vegan"></span><span class="item-calories">770 Calories</span><p class="item-description">Tempeh, rice and beans.</p><div class="item-addon">ADD VEGAN CHEESE FOR $2.00</div><small>Lunch only; taxes additional.</small>`) +
		independentMenuCard("Fish Plate", "$27.00", `<span class="item-calories">240 Calories</span><p class="item-description">Fish in chicken broth; not vegetarian.</p>`) + `</main>`
	text := extractReadableText(markup)
	first := independentMenuCardRow(t, text, "Tempeh Bowl")
	for _, want := range []string{"item-price=11.00;", "770 Calories", "ADD VEGAN CHEESE FOR $2.00", "Lunch only; taxes additional."} {
		if !strings.Contains(first, want) {
			t.Errorf("base card lost or relabeled %q: %q", want, first)
		}
	}
	for _, forbidden := range []string{"item-price=$2.00", "item-price=770", "$11.00", "USD", "Fish Plate", "$27.00", "Reno", "dinner"} {
		if strings.Contains(first, forbidden) {
			t.Errorf("card invented or borrowed %q: %q", forbidden, first)
		}
	}
	second := independentMenuCardRow(t, text, "Fish Plate")
	if !strings.Contains(second, "not vegetarian") || strings.Contains(second, "11.00") || strings.Contains(second, "VEGAN CHEESE") {
		t.Errorf("sibling dietary/price ownership was lost: %q", second)
	}
}

func TestIndependentMenuCardPreservesOnlyExplicitCurrency(t *testing.T) {
	for _, amount := range []string{"11.00", "$11.00", "€11.00", "£11.00", "CAD 11.00", "11.00 AUD"} {
		text := extractReadableText(`<main><h1>Menu</h1>` + independentMenuCard("Garden Plate", amount, `<p>Contains dairy.</p>`) + `</main>`)
		row := independentMenuCardRow(t, text, "Garden Plate")
		if !strings.Contains(row, "item-price="+amount+";") || strings.Contains(row, "USD") {
			t.Errorf("literal price %q acquired or changed currency: %q", amount, row)
		}
	}
}

func TestIndependentMenuCardDropsAmbiguousWholeRecords(t *testing.T) {
	for _, content := range []string{
		`<span class="item-price">11.00</span><span class="item-price">14.00</span>`,
		`<span class="item-price">SMALL 11.00</span><span class="item-price">LARGE 14.00</span>`,
		`<span class="item-calories">770 Calories</span><p>Diameter 11.00 cm.</p>`,
		`<div class="item-addon">Extra cheese <span class="item-price">$2.00</span></div>`,
		`<span class="item-price item-addon">$2.00</span>`,
		`<span class="item-price">11.00</span><div class="item-addon">Extra cheese <span class="item-price">$2.00</span></div>`,
		`<span class="item-price">CAD 11.00 USD</span>`,
		`<span class="item-price">$11.00 EUR</span>`,
		`<span class="item-price">11.00</span>` + independentMenuCard("Nested Special", "29.00", `<p>Child portion; contains peanuts.</p>`),
	} {
		markup := `<main><h1>Menu</h1><section class="menu-item"><h3>Ambiguous Feast</h3>` + content + `<small>Lunch only; contains shellfish.</small></section>` + independentMenuCard("Control Plate", "17.00", `<p>Served with rice.</p>`) + `</main>`
		text := extractReadableText(markup)
		independentMenuCardRow(t, text, "Control Plate")
		for _, forbidden := range []string{"Ambiguous Feast", "Nested Special", "11.00", "14.00", "29.00", "$2.00", "770 Calories"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("ambiguous record leaked partial owner/price %q: %q", forbidden, text)
			}
		}
	}
}

func TestIndependentMenuCardKeepsRestrictiveTextOrDropsWhole(t *testing.T) {
	for _, qualifier := range []string{
		`<small>Harbor branch only; lunch only; contains shellfish; not vegetarian.</small>`,
		`<small aria-hidden="true">Harbor branch only; lunch only; contains shellfish; not vegetarian.</small>`,
	} {
		text := extractReadableText(`<main><h1>Menu</h1>` + independentMenuCard("Garden Plate", "11.00", `<p>Rice and vegetables.</p>`+qualifier) + independentMenuCard("Control Plate", "17.00", `<p>Served with rice.</p>`) + `</main>`)
		independentMenuCardRow(t, text, "Control Plate")
		if strings.Contains(text, "Garden Plate") && !strings.Contains(text, "Harbor branch only; lunch only; contains shellfish; not vegetarian.") {
			t.Errorf("retained dish price lost its restrictive visible text: %q", text)
		}
	}
	oversized := independentMenuCard("Oversized Garden Plate", "11.00", `<p>`+strings.Repeat("葉", maxChunkLength)+`</p><small>Contains shellfish; lunch only.</small>`)
	text := extractReadableText(`<main><h1>Menu</h1>` + oversized + independentMenuCard("Control Plate", "17.00", `<p>Served with rice.</p>`) + `</main>`)
	independentMenuCardRow(t, text, "Control Plate")
	if strings.Contains(text, "Oversized Garden Plate") || strings.Contains(text, "11.00") || strings.Contains(text, "葉") {
		t.Errorf("oversized record was truncated instead of discarded whole: %q", text)
	}
}

func TestIndependentMenuCardAndDisclosureKeepBalancedOwners(t *testing.T) {
	for _, nestedDisclosure := range []bool{false, true} {
		card := independentMenuCard("Garden Plate", "11.00", `<p>Vegetarian rice plate; dinner only.</p>`)
		if nestedDisclosure {
			card = independentMenuCard("Ambiguous Feast", "22.00", `<div class="accordion__tab"><input type="checkbox" id="child"><label class="accordion__tab-label" for="child">Child portions</label><p>Small portion option.</p></div>`)
		}
		markup := `<main><h1>Regional visitor menu</h1><div class="accordion__tab"><input type="checkbox" id="harbor"><label class="accordion__tab-label" for="harbor">Harbor dinner</label>` + card + `</div><p>Regional visitor admission fee is $7 per visitor.</p></main>`
		text := extractReadableText(markup)
		selected := selectQueryChunks("Harbor dinner menu price regional visitor admission fee", text)
		if !strings.Contains(selected, "Section: Regional visitor menu\nRegional visitor admission fee is $7 per visitor.") {
			t.Errorf("menu transformation lost the disclosure end/parent restoration: %q", selected)
		}
		if !nestedDisclosure && !strings.Contains(selected, "Section: Regional visitor menu > Harbor dinner\nMenu item: Garden Plate; item-price=11.00;") {
			t.Errorf("card lost its enclosing disclosure's branch/service owner: %q", selected)
		}
		for _, forbidden := range []string{"REV_EYES_DISCLOSURE_TOKEN_", "[Disclosure start]", "[Disclosure end]", "Ambiguous Feast", "22.00"} {
			if strings.Contains(selected, forbidden) {
				t.Errorf("ambiguous nested scope became a fact or unbalanced outer ownership: %q", selected)
			}
		}
	}
}

func TestIndependentMenuCardHeadingStopsAtItsContainerEnd(t *testing.T) {
	for _, disclosure := range []bool{false, true} {
		before, after := `<h2>Harbor dinner</h2>`, ""
		if disclosure {
			before = `<div class="accordion__tab"><input type="checkbox" id="harbor"><label class="accordion__tab-label" for="harbor">Harbor dinner</label>`
			after = `</div>`
		}
		markup := `<main><h1>Kitchen menu</h1>` + before + `<section><h3>Side sauces</h3><p>Side sauce add-ons cost $3.</p></section>` + independentMenuCard("Garden Plate", "11.00", `<p>Rice and beans.</p>`) + `<p>Harbor parking fee is $4 per visitor.</p>` + after + `</main>`
		selected := selectQueryChunks("Harbor Garden Plate parking fee menu", extractReadableText(markup))
		if !strings.Contains(selected, "Section: Kitchen menu > Harbor dinner\nMenu item: Garden Plate; item-price=11.00;") {
			t.Errorf("card retained a stale sibling owner or lost its own identity: %q", selected)
		}
		if !strings.Contains(selected, "Section: Kitchen menu > Harbor dinner\nHarbor parking fee is $4 per visitor.") {
			t.Errorf("closed card or stale sibling heading acquired later parent text: %q", selected)
		}
	}
}

func TestIndependentMenuCardCapturedTempehPriceNeverBecomesItsAddon(t *testing.T) {
	raw := readWebResearchTestCorpus(t, "reno-primary-menu-acquisition-2026-09-04.json")
	var capture struct {
		Pages []struct {
			URL    string `json:"url"`
			Markup string `json:"page_markup"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(raw, &capture); err != nil {
		t.Fatal(err)
	}
	for _, page := range capture.Pages {
		if page.URL != "https://laughingplanet.com/menu/" {
			continue
		}
		selected := selectQueryChunks("Laughing Planet Tempeh Royale price ingredients", extractReadableText(page.Markup))
		row := independentMenuCardRow(t, selected, "Tempeh Royale")
		for _, want := range []string{"item-price=11.00;", "770 Calories", "Baked organic tempeh", `ADD VEGAN "CHEESE" FOR $2.00`} {
			if !strings.Contains(row, want) {
				t.Errorf("captured primary card lost %q: %q", want, row)
			}
		}
		for _, forbidden := range []string{"item-price=$2.00", "item-price=770", "item-price=$11.00", "USD", "Reno"} {
			if strings.Contains(row, forbidden) {
				t.Errorf("capture acquired unsupported price/currency/branch %q: %q", forbidden, row)
			}
		}
		return
	}
	t.Fatal("expected primary menu capture is absent")
}
