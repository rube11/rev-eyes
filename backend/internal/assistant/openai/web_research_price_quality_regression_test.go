package openai

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/tool/websearch"
)

func TestWebResearchQualityQuotedPricesStayWithCitedSource(t *testing.T) {
	results := []websearch.Result{
		{Title: "Las Vegas comedy and concerts", URL: "https://www.vegas.com/shows", Snippet: "Budget friendly comedy listings. Prices $0-$720. Selected shows from $127."},
		{Title: "Las Vegas show tickets", URL: "https://spotlight.vegas/shows", Snippet: "Shows start from $56.00. Selected comedy from $17."},
		{Title: "Las Vegas Chinatown food guide", URL: "https://www.smartvegasdeals.com/food", Snippet: "Pho Kim Long: Vietnamese pho bowls $12-$14. District One $14–16."},
		{Title: "Las Vegas noodle restaurant menu", URL: "https://menu.example.com/menu", Snippet: "Tonkotsu ramen $17.05. Tasting menu $1,200.00."},
	}
	sources := make(map[string]websearch.Result)
	for _, result := range results {
		sources[result.URL] = result
	}
	tests := []struct {
		name     string
		response string
		passed   bool
		checked  int
	}{
		{"round7 borrowed price with availability caveat", "Vegas.com shows - budget comedy/concert listings, some from $56, but tonight availability isn't verified (vegas.com)", false, 1},
		{"correct actual host", "Shows - some from $56, availability unverified (spotlight.vegas)", true, 1},
		{"same topic is not publisher identity", "Shows - from $56 (Vegas.com)", false, 1},
		{"brand source mapping and quoted guide range", "Pho Kim Long - guide lists bowls about $12-14 (Smart Vegas Deals)", true, 1},
		{"second guide range", "District One - guide lists pho about $14-16 (smartvegasdeals.com)", true, 1},
		{"wrong range endpoint", "Pho Kim Long - guide lists bowls about $12-16 (smartvegasdeals.com)", false, 1},
		{"range cannot span publishers", "Noodles - about $12-17.05 (smartvegasdeals.com; menu.example.com)", false, 1},
		{"numeric substring is not price", "Noodles - from $17 (menu.example.com)", false, 1},
		{"exact cents", "Noodles - listed at $17.05 (menu.example.com)", true, 1},
		{"commas and cents normalized", "Tasting menu - priced at $1200 (menu.example.com)", true, 1},
		{"from lower endpoint in cited range", "Pho - from $12 (smartvegasdeals.com)", true, 1},
		{"quoted approximate price missing", "Noodles - about $19.95 (menu.example.com)", false, 1},
		{"guide quote without about", "Noodles - menu lists bowls $19.95 (menu.example.com)", false, 1},
		{"separate inline citations cannot lend price", "Shows - from $56 (vegas.com); comedy from $17 (spotlight.vegas)", false, 2},
		{"uncited introduction budget not borrowed", "Near your ~$60 target, current menu totals aren't verified.\nPho - about $12-14 (smartvegasdeals.com)", true, 1},
		{"user under budget is not quoted price", "Noodles - a lead for your under $100 budget, total not verified (menu.example.com)", true, 0},
		{"caveated approximate budget", "Noodles - your budget is about $60, actual total not verified (menu.example.com)", true, 0},
		{"budget suffix", "Noodles - near an about $60 target, actual total unverified (menu.example.com)", true, 0},
		{"labeled inferred arithmetic", "Noodles - two bowls would cost about $34.10 before tax; calculated from $17.05 each (menu.example.com)", true, 0},
		{"estimate is not literal quote", "Noodles - estimated total around $40 for two before tip (menu.example.com)", true, 0},
		{"unlabeled arithmetic outside narrow quote grammar", "Noodles - $17.05 each, two cost $34.10 before tax (menu.example.com)", true, 0},
		{"budget caveat does not excuse explicit guide quote", "Noodles - near your $60 target; guide lists bowls about $99 (menu.example.com)", false, 1},
		{"earlier estimate does not excuse later quote", "My estimate is not verified. Noodles - about $99 (menu.example.com)", false, 1},
		{"single labeled inline publisher", "Comedy starts at $56. Source: Vegas.com", false, 1},
		{"exact source URL", "Comedy - from $56 (https://spotlight.vegas/shows)", true, 1},
		{"exact URL restricts price to page", "Comedy - from $56 (https://www.vegas.com/shows)", false, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check := qualityQuotedPriceCitationCheck(tt.response, sources)
			if check.Passed != tt.passed {
				t.Fatalf("passed = %v, want %v: %s", check.Passed, tt.passed, check.Detail)
			}
			wantPrefix := fmt.Sprintf("%d explicit", tt.checked)
			if !strings.HasPrefix(check.Detail, wantPrefix) {
				t.Fatalf("want %d screened expressions: %s", tt.checked, check.Detail)
			}
		})
	}
}

func TestWebResearchQualityQuotedPriceGateIntegrated(t *testing.T) {
	search := qualityFixtureSearch(t, []websearch.Result{
		{Title: "Las Vegas ramen restaurants", URL: "https://www.example.com/menu", Snippet: "Ramen bowls from $17.05."},
		{Title: "Las Vegas ramen restaurants", URL: "https://different.example/menu", Snippet: "Ramen bowls from $56."},
	}, nil, "Las Vegas ramen")
	assessment := evaluateLiveSearchQuality(liveEyesWebScenario{name: "las_vegas_restaurants_for_jolene"},
		"Noodles - guide lists bowls from $56 (example.com)", []liveComparisonSearch{search}, qualityFixtureClock())
	if assessment.MinimumGatePassed {
		t.Fatal("cross-publisher price passed integrated minimum gates")
	}
	found := false
	for _, check := range assessment.Checks {
		if check.Name == "quoted_price_citation_support" {
			found = true
			if check.Passed {
				t.Fatal("incorrect quoted price attribution was not detected")
			}
		}
	}
	if !found || len(assessment.ManualReviewRequired) < 3 {
		t.Fatal("price screen and its manual-review limitation must both be present")
	}
}

func TestWebResearchQualityQuotedPriceExactURLDoesNotBroadenToHost(t *testing.T) {
	sources := map[string]websearch.Result{
		"https://example.com/one": {URL: "https://example.com/one", Snippet: "From $10."},
		"https://example.com/two": {URL: "https://example.com/two", Snippet: "From $56."},
	}
	if qualityQuotedPriceCitationCheck("Comedy - from $56 (https://example.com/one)", sources).Passed {
		t.Fatal("exact URL citation borrowed another page's price")
	}
	if !qualityQuotedPriceCitationCheck("Comedy - from $56 (example.com)", sources).Passed {
		t.Fatal("publisher-only citation should allow that publisher's retrieved pages")
	}
}
