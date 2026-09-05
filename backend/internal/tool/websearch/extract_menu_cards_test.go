package websearch

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMenuCardsKeepOwnedBasePriceDescriptionAndLiteralRestrictions(t *testing.T) {
	markup := `<main><h1>Juniper menu</h1><h2>Lunch</h2>
<section class="menu-item menu-item-full"><h3>Tempeh Royale</h3><span class="item-price">11.00</span><span class="item-calories">770 Calories</span><p class="item-description">Baked tempeh and rice.</p><div class="item-addon">ADD VEGAN "CHEESE" FOR $2.00</div><small>Contains peanuts; not suitable for a nut allergy. Lunch only at Harbor.</small></section>
<section class="menu-item"><h3>Fish Bowl</h3><span class="item-price">24.00</span><p>Fish broth; not vegetarian.</p></section></main>`
	text := extractReadableText(markup)
	for _, row := range strings.Split(text, "\n") {
		if strings.HasPrefix(row, menuItemPrefix+"Tempeh Royale") {
			for _, want := range []string{"item-price=11.00", "770 Calories", "Baked tempeh and rice.", `ADD VEGAN "CHEESE" FOR $2.00`, "Contains peanuts; not suitable for a nut allergy.", "Lunch only at Harbor."} {
				if !strings.Contains(row, want) {
					t.Errorf("card lost owned text %q: %q", want, row)
				}
			}
			for _, forbidden := range []string{"Fish Bowl", "24.00", "$11.00", "USD", "vegetarian", "dinner"} {
				if strings.Contains(row, forbidden) {
					t.Errorf("card invented/borrowed %q: %q", forbidden, row)
				}
			}
		}
	}
	excerpt := selectQueryChunks("Juniper Tempeh Royale menu price", text)
	if !strings.Contains(excerpt, "Menu item: Tempeh Royale; item-price=11.00;") || !strings.Contains(excerpt, "Section: Juniper menu > Lunch\n") || !strings.Contains(excerpt, "Lunch only at Harbor.") {
		t.Fatalf("selector lost complete owned card: %q", excerpt)
	}
}

func TestMenuCardsAcceptOnlyWholeLiteralPriceFields(t *testing.T) {
	for _, amount := range []string{"11.00", "11", "$11.00", "€ 11.00", "£11.00", "USD 11.00", "11.00 CAD"} {
		t.Run(amount, func(t *testing.T) {
			text := extractReadableText(`<section class="menu-item"><h3>Garden Plate</h3><span class="item-price">` + amount + `</span></section>`)
			if want := "[Heading 3]\n" + menuItemPrefix + "Garden Plate; item-price=" + amount; text != want {
				t.Fatalf("literal price changed: got %q, want %q", text, want)
			}
		})
	}
	for _, amount := range []string{"770 Calories", "11.00 12.00", "SMALL 6.00 LARGE 10.00", "USD 11.00 EUR", "from 11.00", "11.00 per gram", "11.00%", "11.001", ""} {
		t.Run("unsupported_"+amount, func(t *testing.T) {
			text := extractReadableText(`<section class="menu-item"><h3>Ambiguous Plate</h3><span class="item-price">` + amount + `</span><small>Late restriction.</small></section><p>Unrelated page content survives.</p>`)
			if strings.Contains(text, "Ambiguous Plate") || strings.Contains(text, "Late restriction") || !strings.Contains(text, "Unrelated page content survives.") {
				t.Fatalf("unsupported price card survived partially: %q", text)
			}
		})
	}
}

func TestMenuCardsDoNotInferPriceFromCaloriesOrDietFromEmptyIcons(t *testing.T) {
	markup := `<section class="menu-item"><h3>Garden Plate</h3><span class="item-price">11.00</span><span class="item-calories">770 Calories</span><span class="icon-allergy-vegan"></span><span class="icon-allergy-gluten"></span><span class="icon-allergy-nuts"></span></section>`
	text := extractReadableText(markup)
	if text != "[Heading 3]\nMenu item: Garden Plate; item-price=11.00; 770 Calories" {
		t.Fatalf("empty icon class became a dietary claim or calorie became price: %q", text)
	}
	for _, markup := range []string{
		`<section class="menu-item"><h3>Unpriced Plate</h3><span class="item-calories">770 Calories</span><p>Coordinates 11.00, 12.00.</p></section>`,
		`<section class="menu-item-extra"><h3>Lookalike Card</h3><span class="item-price">11.00</span></section>`,
		`<section class="menu-item"><h3>Lookalike Price</h3><span class="item-price-old">11.00</span></section>`,
		`<div class="menu-item"><h3>Unsupported Container</h3><span class="item-price">11.00</span></div>`,
	} {
		if text := extractReadableText(markup); strings.Contains(text, menuItemPrefix) {
			t.Errorf("unsupported structure acquired a menu price owner: %q", text)
		}
	}
	for _, text := range []string{"11.00", "770 Calories", "Coordinates 11.00, 12.00", "item-price=11.00"} {
		if hasPriceEvidence(text) {
			t.Errorf("general numeric evidence was broadened: %q", text)
		}
	}
}

func TestMenuCardsDropNestedConflictingAndAddonOnlyPricesWhole(t *testing.T) {
	for _, body := range []string{
		`<h3>Ambiguous Plate</h3><span class="item-price">11.00</span><span class="item-price">12.00</span>`,
		`<h3>Ambiguous Plate</h3><h4>Another Plate</h4><span class="item-price">11.00</span>`,
		`<h3>Ambiguous Plate</h3><section class="menu-item"><h3>Child Plate</h3><span class="item-price">12.00</span></section>`,
		`<h3>Ambiguous Plate</h3><span class="item-price">11.00</span><div class="item-addon">Add cheese <span class="item-price">$2.00</span></div>`,
		`<h3>Ambiguous Plate</h3><div class="item-addon">Add cheese <span class="item-price">$2.00</span></div>`,
	} {
		text := extractReadableText(`<section class="menu-item">` + body + `<small>Late restriction.</small></section><p>Unrelated page content survives.</p>`)
		if text != "Unrelated page content survives." {
			t.Errorf("ambiguous card leaked a partial item or borrowed add-on: %q", text)
		}
	}
}

func TestMenuCardsAtomicRuneLimitRetainsOrDropsLateNegation(t *testing.T) {
	const start, end = "Menu item: Garden Plate; item-price=11.00; ", " Contains shellfish; not vegetarian."
	for _, length := range []int{maxChunkLength, maxChunkLength + 1} {
		body := strings.Repeat("葉", length-utf8.RuneCountInString(start+end)) + end
		markup := `<section class="menu-item"><h3>Garden Plate</h3><span class="item-price">11.00</span><p>` + body + `</p></section>`
		text := extractReadableText(markup)
		if length == maxChunkLength {
			row := strings.TrimPrefix(text, "[Heading 3]\n")
			if row != start+body || utf8.RuneCountInString(row) != maxChunkLength || !strings.Contains(selectQueryChunks("Garden Plate price", text), end) {
				t.Fatalf("exactly bounded card lost its late negation: %q", text)
			}
		} else if text != "" {
			t.Fatalf("oversized card was truncated rather than dropped: %q", text)
		}
	}
}

func TestMenuCardsPriceQueryPrefersOwnedBasePriceToStandaloneAddons(t *testing.T) {
	markup := `<main><h1>Garden menu</h1><p>Add cheese for $2.00.</p><p>Add salsa for $1.00.</p><p>Add guacamole for $3.00.</p><section class="menu-item"><h3>Garden Plate</h3><span class="item-price">11.00</span><p>Rice and beans.</p></section></main>`
	excerpt := selectQueryChunks("Garden menu prices", extractReadableText(markup))
	if !strings.Contains(excerpt, "Menu item: Garden Plate; item-price=11.00; Rice and beans.") {
		t.Fatalf("standalone add-ons crowded out the complete base-priced item: %q", excerpt)
	}
}

func TestMenuCardsCapturedLaughingPlanetNamedPriceSurvivesSelection(t *testing.T) {
	data := readWebResearchTestCorpus(t, "reno-primary-menu-acquisition-2026-09-04.json")
	var capture struct {
		Query string `json:"query"`
		Pages []struct {
			URL        string `json:"url"`
			PageMarkup string `json:"page_markup"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(data, &capture); err != nil {
		t.Fatal(err)
	}
	for _, page := range capture.Pages {
		if page.URL != "https://laughingplanet.com/menu/" {
			continue
		}
		text := extractReadableText(page.PageMarkup)
		focused := selectQueryChunks("Laughing Planet Tempeh Royale price ingredients", text)
		found := false
		for _, block := range strings.Split(focused, "\n\n") {
			if !strings.Contains(block, "Menu item: Tempeh Royale;") {
				continue
			}
			found = true
			for _, want := range []string{"item-price=11.00", "770 Calories", "Baked organic tempeh, brown rice", `ADD VEGAN "CHEESE" FOR $2.00`} {
				if !strings.Contains(block, want) {
					t.Errorf("captured card lost %q: %q", want, block)
				}
			}
			for _, forbidden := range []string{"$11.00", "USD", "Reno", "dinner", "vegetarian", "icon-allergy"} {
				if strings.Contains(block, forbidden) {
					t.Errorf("captured card acquired unsupported scope/diet/currency %q: %q", forbidden, block)
				}
			}
		}
		if !found {
			t.Fatalf("named-item search still selects add-on without base price: %q", focused)
		}
		broad := selectQueryChunks(capture.Query, text)
		if !strings.Contains(broad, "Menu item: ") || !strings.Contains(broad, "item-price=") {
			t.Fatalf("broad captured search still contains only detached add-ons: %q", broad)
		}
		t.Logf("broad captured query:\n%s\nfocused named-item query:\n%s", broad, focused)
		return
	}
	t.Fatal("Laughing Planet page missing from immutable capture")
}
