package websearch

import (
	"context"
	"strings"
	"testing"
)

func TestMenuItemLinksKeepVegetarianDishPriceDescriptionAndSectionTogether(t *testing.T) {
	markup := `<main><h1>Juniper Kitchen Menu</h1><h2>Evening</h2><p>5 PM - 10 PM</p>
<a href="/menu?item=roast"><div>Slow Roasted Chicken</div><div>$31.00</div><p>Served with gravy.</p></a>
<a href="/menu?item=fish"><div>Market Fish</div><div>$29.00</div><p>Served with lemon.</p></a>
<a href="/menu?item=beef"><div>Grilled Beef</div><div>$38.00</div><p>Served with potatoes.</p></a>
<a href="/menu?item=lentils"><div>Vegetarian Lentil Plate</div><div>$19.00</div><p>Lentils, carrots and rice. Evening only; tax and gratuity additional.</p></a>
<h2>Breakfast</h2><p>7 AM - 11 AM</p>
<a href="/menu?item=breakfast"><div>Vegetarian Morning Bowl</div><div>$14.00</div><p>Eggs, potatoes and herbs.</p></a></main>`
	extractor, endpoint := evidenceTestExtractor(t, markup)
	got, _ := extractor.enrich(context.Background(), "Juniper Kitchen vegetarian dinner menu prices", []Result{{URL: endpoint}})
	found := false
	for _, block := range got[0].PageExcerpts {
		if strings.Contains(block, "Vegetarian Lentil Plate") {
			found = true
			for _, want := range []string{"Section: Juniper Kitchen Menu > Evening", "Section service period: 5 PM - 10 PM", "$19.00", "Lentils, carrots and rice.", "Evening only; tax and gratuity additional."} {
				if !strings.Contains(block, want) {
					t.Errorf("item lost %q: %q", want, block)
				}
			}
			for _, other := range []string{"Chicken", "Market Fish", "Grilled Beef", "Breakfast", "$14.00"} {
				if strings.Contains(block, other) {
					t.Errorf("item borrowed sibling field %q: %q", other, block)
				}
			}
		}
		if strings.Contains(block, "Vegetarian Morning Bowl") && (strings.Contains(block, "5 PM") || !strings.Contains(block, "7 AM - 11 AM")) {
			t.Errorf("service period crossed a sibling section: %q", block)
		}
	}
	if !found {
		t.Fatalf("diet-relevant full item crowded out by anonymous prices: %#v", got)
	}
}

func TestStructuredMenuItemDietaryDescriptionStaysWithOwnPrice(t *testing.T) {
	markup := `<script type="application/ld+json">{"@type":"Menu","name":"Evening Dining","hasMenuSection":{"@type":"MenuSection","name":"Plates","hasMenuItem":[
{"@type":"MenuItem","name":"Garden Plate","description":"Vegetarian lentils with herbs; dinner only.","suitableForDiet":"https://schema.org/VegetarianDiet","offers":{"price":19,"priceCurrency":"USD"}},
{"@type":"MenuItem","name":"Market Fish","description":"Wild fish with butter.","offers":{"price":29,"priceCurrency":"USD"}}
]}}</script>`
	got := extractStructuredEvidence(markup)
	for _, row := range strings.Split(got, "\n") {
		if strings.Contains(row, "Garden Plate") {
			for _, want := range []string{"parent=Evening Dining > Plates", "description=Vegetarian lentils with herbs; dinner only.", "suitableForDiet=https://schema.org/VegetarianDiet", "offer.price=$19"} {
				if !strings.Contains(row, want) {
					t.Errorf("same-item field %q lost: %q", want, row)
				}
			}
		}
		if strings.Contains(row, "Market Fish") && (strings.Contains(row, "Vegetarian") || strings.Contains(row, "$19")) {
			t.Errorf("sibling dish borrowed diet or price: %q", row)
		}
	}
	if !strings.Contains(got, "Garden Plate") || !strings.Contains(got, "Market Fish") {
		t.Fatalf("expected both separate menu records: %q", got)
	}
}
