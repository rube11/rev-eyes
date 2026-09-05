package websearch

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestIndependentMenuOwnershipKeepsNestedItemsWithBranchAndServicePeriod(t *testing.T) {
	markup := `<main><h1>Seabird Kitchen menus</h1><h2>Harbor branch</h2><h3>Dinner</h3><div>5 PM - 10 PM</div>
<a href="/menus?item=pea-harbor"><div><span>Pea Garden Plate</span></div><div>$18.00</div><div>Vegetarian peas and rice; dinner only.</div></a>
<a href="/menu/items/fish"><div>Harbor Fish Plate</div><div>$31.00</div><div>Fish with lemon butter; not vegetarian.</div></a>
<h2>Hillside branch</h2><h3>Lunch</h3><div>11 AM - 2 PM</div>
<a href="/menus?item_id=pea-hillside"><div>Pea Garden Plate</div><div>$14.00</div><div>Lunch portion only; service charge extra.</div></a></main>`
	excerpt := selectQueryChunks("Seabird Kitchen menu prices", extractReadableText(markup))
	for _, test := range []struct{ name, price, section, hours, qualifier, forbidden string }{
		{"Pea Garden Plate", "$18.00", "Harbor branch > Dinner", "5 PM - 10 PM", "Vegetarian peas and rice; dinner only.", "$14.00"},
		{"Harbor Fish Plate", "$31.00", "Harbor branch > Dinner", "5 PM - 10 PM", "Fish with lemon butter; not vegetarian.", "Pea Garden Plate"},
		{"Pea Garden Plate", "$14.00", "Hillside branch > Lunch", "11 AM - 2 PM", "Lunch portion only; service charge extra.", "$18.00"},
	} {
		found := false
		for _, block := range strings.Split(excerpt, "\n\n") {
			if !strings.Contains(block, "Menu item: "+test.name) || !strings.Contains(block, test.price) {
				continue
			}
			found = true
			for _, want := range []string{"Section: Seabird Kitchen menus > " + test.section, "Section service period: " + test.hours, test.qualifier} {
				if !strings.Contains(block, want) {
					t.Errorf("item lost its source-owned field %q: %q", want, block)
				}
			}
			if strings.Contains(block, test.forbidden) {
				t.Errorf("item borrowed a sibling price/identity: %q", block)
			}
		}
		if !found {
			t.Errorf("item %q/%s was fragmented or omitted: %q", test.name, test.price, excerpt)
		}
	}
}

func TestIndependentMenuOwnershipDoesNotCarryServiceHoursIntoSiblingSection(t *testing.T) {
	markup := `<main><h1>Seabird Kitchen</h1><h2>Lunch</h2><p>11 AM - 2 PM</p>
<a href="/menu?item=lunch"><div>Lunch Grain Bowl</div><div>$13</div></a>
<h2>Dinner</h2><a href="/menu?item=dinner"><div>Dinner Grain Bowl</div><div>$19</div><p>Evening portion.</p></a></main>`
	excerpt := selectQueryChunks("Seabird Kitchen dinner menu prices", extractReadableText(markup))
	found := false
	for _, block := range strings.Split(excerpt, "\n\n") {
		if strings.Contains(block, "Menu item: Dinner Grain Bowl") {
			found = true
			if strings.Contains(block, "11 AM") || strings.Contains(block, "Section service period:") || !strings.Contains(block, "Section: Seabird Kitchen > Dinner") {
				t.Fatalf("dinner item inherited a lunch-only service window: %q", block)
			}
		}
	}
	if !found {
		t.Fatalf("dinner fixture was not selected: %q", excerpt)
	}
}

func TestIndependentMenuOwnershipDropsOversizedItemRatherThanLateQualifier(t *testing.T) {
	markup := `<main><h1>Seabird Kitchen</h1><a href="/menu?item=oversize"><div>Long Garden Feast</div><div>$18</div><p>` + strings.Repeat("Detailed preparation notes. ", 30) + `Contains shellfish; not vegetarian.</p></a><p>Other restaurant information remains available.</p></main>`
	text := extractReadableText(markup)
	for _, forbidden := range []string{"Long Garden Feast", "$18", "Detailed preparation notes", "Contains shellfish"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("oversized item leaked a partial identity/price/qualifier: %q", text)
		}
	}
	if !strings.Contains(text, "Other restaurant information remains available.") {
		t.Fatal("discarding an oversized item removed unrelated page text")
	}
}

func TestIndependentMenuOwnershipUsesAtomicRuneBoundary(t *testing.T) {
	const start, end = "Garden Plate $18 ", " contains peanuts."
	for _, limit := range []int{maxChunkLength, maxChunkLength + 1} {
		label := start + strings.Repeat("葉", limit-utf8.RuneCountInString(menuItemPrefix+start+end)) + end
		text := extractReadableText(`<main><a href="/menu/items/garden">` + label + `</a></main>`)
		if limit == maxChunkLength {
			if text != menuItemPrefix+label || utf8.RuneCountInString(text) != maxChunkLength {
				t.Fatalf("exactly bounded item was changed: %d runes %q", utf8.RuneCountInString(text), text)
			}
		} else if text != "" {
			t.Fatalf("one-rune-over item was truncated instead of discarded: %q", text)
		}
	}
}

func TestIndependentMenuOwnershipDoesNotPromoteNavigationOrGenericPriceLinks(t *testing.T) {
	for _, markup := range []string{
		`<main><nav><a href="/menu?item=nav">Navigation Plate $18</a></nav></main>`,
		`<main><a href="/menu">Full menu from $18</a></main>`,
		`<main><a href="/catalog?item=boots">Walking Boots $18</a></main>`,
		`<main><a href="/menu?item=">Unspecified Menu Item $18</a></main>`,
		`<main><a href="/menus/items/">Missing Item Identity $18</a></main>`,
		`<main><a href="javascript:showMenu()">Open menu $18</a></main>`,
	} {
		if text := extractReadableText(markup); strings.Contains(text, menuItemPrefix) {
			t.Errorf("non-item link acquired an atomic menu-item marker: %q", text)
		}
	}
	for _, markup := range []string{
		`<main><a href="/menu?item=anonymous">$18</a></main>`,
		`<main><a itemtype="https://schema.org/MenuItem">$18</a></main>`,
	} {
		if text := extractReadableText(markup); text != "" {
			t.Errorf("price without an item identity was retained as an atomic menu row: %q", text)
		}
	}
}

func TestIndependentMenuOwnershipStructuredDietAndDescriptionStayOnTheirItem(t *testing.T) {
	markup := `<script type="application/ld+json">{"@type":"Restaurant","name":"Seabird Harbor","suitableForDiet":"https://schema.org/VeganDiet","hasMenu":{"@type":"Menu","name":"Dinner","hasMenuSection":{"@type":"MenuSection","name":"Main plates","hasMenuItem":[
{"@type":"MenuItem","name":"Pea Garden Plate","description":"Peas with rice; contains dairy; dinner only.","suitableForDiet":"https://schema.org/VegetarianDiet","offers":{"price":18,"priceCurrency":"USD"}},
{"@type":"MenuItem","name":"Mushroom Broth","description":"Mushrooms in chicken broth; lunch portion.","offers":{"price":12,"priceCurrency":"USD"}}
]}}}</script>`
	text := extractStructuredEvidence(markup)
	rows := strings.Split(text, "\n")
	foundPea, foundBroth := false, false
	for _, row := range rows {
		if strings.Contains(row, "MenuItem Pea Garden Plate;") {
			foundPea = true
			for _, want := range []string{"parent=Seabird Harbor > Dinner > Main plates", "description=Peas with rice; contains dairy; dinner only.", "suitableForDiet=https://schema.org/VegetarianDiet", "offer.price=$18"} {
				if !strings.Contains(row, want) {
					t.Errorf("explicit same-item information %q missing: %q", want, row)
				}
			}
			if strings.Contains(row, "VeganDiet") || strings.Contains(row, "$12") || strings.Contains(row, "chicken broth") {
				t.Errorf("item inherited parent/sibling diet, price or description: %q", row)
			}
		}
		if strings.Contains(row, "MenuItem Mushroom Broth;") {
			foundBroth = true
			if strings.Contains(row, "suitableForDiet=") || strings.Contains(row, "$18") || !strings.Contains(row, "chicken broth; lunch portion.") || !strings.Contains(row, "offer.price=$12") {
				t.Errorf("mushroom name/parent/sibling caused diet inference or ownership transfer: %q", row)
			}
		}
	}
	if !foundPea || !foundBroth {
		t.Fatalf("explicit sibling menu records were lost: %q", text)
	}
}

func TestIndependentMenuOwnershipStructuredOversizedQualifiersDiscardWholeItem(t *testing.T) {
	for _, test := range []struct{ name, description, diet string }{
		{"description_over_limit", strings.Repeat("葉", 301), ""},
		{"diet_over_limit", "Contains dairy.", strings.Repeat("x", 121)},
		{"whole_record_over_limit", strings.Repeat("葉", 300), strings.Repeat("x", 120)},
	} {
		t.Run(test.name, func(t *testing.T) {
			oversize := map[string]any{"@type": "MenuItem", "name": "Oversized Garden Plate", "description": test.description, "offers": map[string]any{"price": 18, "priceCurrency": "USD"}}
			if test.diet != "" {
				oversize["suitableForDiet"] = test.diet
			}
			data, _ := json.Marshal([]any{oversize, map[string]any{"@type": "MenuItem", "name": "Simple Pea Plate", "description": "Contains dairy.", "offers": map[string]any{"price": 16, "priceCurrency": "USD"}}})
			text := extractStructuredEvidence(`<script type="application/ld+json">` + string(data) + `</script>`)
			if strings.Contains(text, "Oversized Garden Plate") || strings.Contains(text, "offer.price=$18") || !strings.Contains(text, "Simple Pea Plate") || !strings.Contains(text, "Contains dairy.") {
				t.Fatalf("oversized item's price survived without its qualifiers or a separate sibling was lost: %q", text)
			}
		})
	}
}
