package websearch

import (
	"strings"
	"testing"
)

func TestShortEvidenceRetainsMenuPriceUnderItsItemHeading(t *testing.T) {
	t.Parallel()
	for _, price := range []string{"$10.00", "€12.50", "£9.00"} {
		t.Run(price, func(t *testing.T) {
			markup := `<main><h1>Fern Kitchen</h1><h2>Menu</h2><h3>Lentil Bowl</h3><p>` + price + `</p></main>`
			got := selectQueryChunks("Fern Kitchen menu price", extractReadableText(markup))
			want := "Section: Fern Kitchen > Menu > Lentil Bowl\n" + price
			if got != want {
				t.Errorf("short price lost item ownership: got %q, want %q", got, want)
			}
		})
	}
}

func TestShortEvidenceRetainsClockOnlyForOwnedTimingQuery(t *testing.T) {
	t.Parallel()
	for _, query := range []string{"Cedar Room hours", "Cedar Room schedule", "Cedar Room closing"} {
		t.Run(query, func(t *testing.T) {
			markup := `<main><h1>Cedar Room</h1><h2>Friday live music</h2><p>8pm–11pm</p></main>`
			got := selectQueryChunks(query, extractReadableText(markup))
			want := "Section: Cedar Room > Friday live music\n8pm–11pm"
			if got != want {
				t.Errorf("short clock lost activity/day ownership: got %q, want %q", got, want)
			}
		})
	}
}

func TestShortEvidenceDoesNotAdmitBareOrUnrelatedNumbers(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct{ name, query, markup string }{
		{"unowned_price", "price $10.00", `<p>$10.00</p>`},
		{"unowned_clock", "hours 8pm 11pm", `<p>8pm–11pm</p>`},
		{"empty_heading", "menu price", `<h2> </h2><p>$10.00</p>`},
		{"price_with_nonprice_query", "Fern Kitchen address", `<h1>Fern Kitchen</h1><h2>Menu</h2><p>$10.00</p>`},
		{"clock_with_nontiming_query", "Cedar Room address", `<h1>Cedar Room</h1><h2>Live music</h2><p>8pm–11pm</p>`},
		{"clock_for_price_query", "Cedar Room menu prices", `<h1>Cedar Room</h1><p>8pm–11pm</p>`},
		{"price_for_timing_query", "Fern Kitchen hours", `<h1>Fern Kitchen</h1><p>$10.00</p>`},
		{"unrelated_heading", "Fern Kitchen menu prices", `<h1>Warehouse inventory</h1><p>$10.00</p>`},
		{"unmarked_number", "Fern Kitchen menu prices", `<h1>Fern Kitchen</h1><h2>Menu</h2><p>10.00</p>`},
		{"short_prose", "Fern Kitchen opening hours", `<h1>Fern Kitchen</h1><p>Open daily</p>`},
		{"short_numeric_prose", "Fern Kitchen menu prices", `<h1>Fern Kitchen</h1><p>Top 10</p>`},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if got := selectQueryChunks(scenario.query, extractReadableText(scenario.markup)); got != "" {
				t.Errorf("unowned or unrelated short line became evidence: %q", got)
			}
		})
	}
}

func TestShortEvidenceKeepsSiblingMenuItemPricesSeparate(t *testing.T) {
	t.Parallel()
	markup := `<main><h1>Fern Kitchen</h1><h2>Menu</h2>
<h3>Lentil Bowl</h3><h4>Large</h4><p>$10.00</p>
<h3>Bean Soup</h3><p>$7.50</p>
<h3>Garden Salad</h3><p>$8.00</p></main>`
	got := selectQueryChunks("Fern Kitchen menu prices", extractReadableText(markup))
	blocks := strings.Split(got, "\n\n")
	wants := []string{
		"Section: Fern Kitchen > Menu > Lentil Bowl > Large\n$10.00",
		"Section: Fern Kitchen > Menu > Bean Soup\n$7.50",
		"Section: Fern Kitchen > Menu > Garden Salad\n$8.00",
	}
	if len(blocks) != len(wants) {
		t.Fatalf("wanted three separate item-price blocks, got %q", got)
	}
	for index, want := range wants {
		if blocks[index] != want {
			t.Errorf("item %d cross-merged sibling price/size heading: got %q, want %q", index, blocks[index], want)
		}
	}
}

func TestShortEvidenceDoesNotJoinOwnershipAcrossSources(t *testing.T) {
	t.Parallel()
	query := "Fern Kitchen menu price $10.00"
	first := selectQueryChunks(query, extractReadableText(`<h1>Fern Kitchen</h1><h2>Lentil Bowl</h2>`))
	second := selectQueryChunks(query, extractReadableText(`<p>$10.00</p>`))
	if first != "" || second != "" {
		t.Errorf("heading-only source lent ownership to an unrelated source: first=%q second=%q", first, second)
	}
}

func TestShortEvidenceLeavesAtomicStructuredRowsUnchanged(t *testing.T) {
	t.Parallel()
	const row = "Page structured data: MenuItem Lentil Bowl; parent=Fern Kitchen > Menu; offer.price=$10.00; offer.priceCurrency=USD; offer.availability=https://schema.org/SoldOut"
	text := "[Heading 1] Other Establishment\n" + row
	got := selectQueryChunks("Fern Kitchen menu price", text)
	if got != row {
		t.Errorf("existing structured row changed or inherited an unrelated heading: got %q, want %q", got, row)
	}
}
