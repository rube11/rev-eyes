package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"
)

func structuredTestMarkup(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return `<script type="application/ld+json">` + string(encoded) + `</script>`
}

func structuredTestEvent(name string) map[string]any {
	return map[string]any{
		"@type": "MusicEvent", "name": name,
		"startDate": "2026-10-02T20:00:00-04:00", "endDate": "2026-10-02T22:00:00-04:00",
	}
}

func TestStructuredEvidenceEventKeepsOwnedFieldsTogether(t *testing.T) {
	t.Parallel()
	event := structuredTestEvent("Copper Quartet")
	event["eventStatus"] = "https://schema.org/EventCancelled"
	event["isAccessibleForFree"] = false
	event["location"] = map[string]any{"@type": "Place", "name": "Cedar Hall", "address": map[string]any{"streetAddress": "14 Grove Lane", "addressLocality": "Mapleton"}}
	event["offers"] = map[string]any{"@type": "Offer", "price": 18.50, "priceCurrency": "USD", "availability": "https://schema.org/SoldOut"}
	got := extractStructuredEvidence(structuredTestMarkup(event))
	for _, want := range []string{"MusicEvent Copper Quartet", "EventCancelled", "startDate=2026-10-02T20:00:00-04:00", "endDate=2026-10-02T22:00:00-04:00", "Cedar Hall, 14 Grove Lane, Mapleton", "entry is not free", "offer.price=$18.5", "offer.priceCurrency=USD", "SoldOut"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing event-owned field %q from %q", want, got)
		}
	}
	if strings.Contains(got, "\n") {
		t.Errorf("single event was split into detached claims: %q", got)
	}
}

func TestStructuredEvidenceVenueHoursNeverBecomeEventHours(t *testing.T) {
	t.Parallel()
	event := structuredTestEvent("Juniper Trio")
	venue := map[string]any{
		"@type": "BarOrPub", "name": "Elm Room", "subEvent": event,
		"openingHoursSpecification":        map[string]any{"dayOfWeek": []any{"Friday", "Saturday"}, "opens": "17:00", "closes": "02:00"},
		"specialOpeningHoursSpecification": map[string]any{"validFrom": "2026-10-10", "validThrough": "2026-10-10", "opens": "00:00", "closes": "00:00"},
	}
	got := extractStructuredEvidence(structuredTestMarkup(venue))
	var venueHours, specialHours, eventLine string
	for _, line := range strings.Split(got, "\n") {
		switch {
		case strings.Contains(line, "Juniper Trio"):
			eventLine = line
		case strings.Contains(line, "venue openingHoursSpecification"):
			venueHours = line
		case strings.Contains(line, "venue specialOpeningHoursSpecification"):
			specialHours = line
		}
	}
	if !strings.Contains(venueHours, "Elm Room") || !strings.Contains(venueHours, "Friday, Saturday") || !strings.Contains(venueHours, "closes=02:00") {
		t.Errorf("lost venue-owned hours: %q", got)
	}
	if !strings.Contains(specialHours, "validThrough=2026-10-10") || !strings.Contains(specialHours, "closes=00:00") {
		t.Errorf("lost special-hours qualifiers: %q", got)
	}
	if !strings.Contains(eventLine, "parent=Elm Room") || !strings.Contains(eventLine, "endDate=2026-10-02T22:00:00-04:00") || strings.Contains(eventLine, "02:00") || strings.Contains(eventLine, "Friday") {
		t.Errorf("event did not retain independent interval and ownership: %q", eventLine)
	}
}

func TestStructuredEvidenceMenuPricesStayWithTheirItemAndOffer(t *testing.T) {
	t.Parallel()
	menu := map[string]any{
		"@type": "Restaurant", "name": "Clover Kitchen", "hasMenu": map[string]any{
			"@type": "Menu", "name": "Dinner", "hasMenuSection": map[string]any{
				"@type": "MenuSection", "name": "Bowls", "hasMenuItem": []any{
					map[string]any{"@type": "MenuItem", "name": "Mushroom Bowl", "offers": []any{
						map[string]any{"name": "Lunch", "price": 12, "priceCurrency": "USD", "validThrough": "2026-10-01"},
						map[string]any{"name": "Dinner", "price": 17, "priceCurrency": "USD"},
					}},
					map[string]any{"@type": "MenuItem", "name": "Tofu Bowl", "offers": map[string]any{"price": 14, "priceCurrency": "USD"}},
				},
			},
		},
	}
	got := extractStructuredEvidence(structuredTestMarkup(menu))
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("wanted three isolated menu offers, got %q", got)
	}
	for _, line := range lines {
		if !strings.Contains(line, "Clover Kitchen") {
			t.Errorf("menu item lost restaurant ownership through nested menus: %q", line)
		}
		if !strings.Contains(line, "parent=") || strings.Contains(line, "Mushroom Bowl") && strings.Contains(line, "offer.price=$14") || strings.Contains(line, "Tofu Bowl") && (strings.Contains(line, "offer.price=$12") || strings.Contains(line, "offer.price=$17")) {
			t.Errorf("cross-merged menu price ownership: %q", line)
		}
		if strings.Contains(line, "offer.price=$12") && (!strings.Contains(line, "offer.name=Lunch") || !strings.Contains(line, "offer.validThrough=2026-10-01")) {
			t.Errorf("lost lunch price qualifier: %q", line)
		}
		if strings.Contains(line, "offer.price=$17") && strings.Contains(line, "2026-10-01") {
			t.Errorf("borrowed another offer's validity: %q", line)
		}
	}
}

func TestStructuredEvidenceOversizedRecordIsDiscardedRatherThanWindowed(t *testing.T) {
	t.Parallel()
	event := structuredTestEvent(strings.Repeat("A", 90))
	event["eventStatus"] = strings.Repeat("B", 70)
	event["location"] = strings.Repeat("C", 100)
	event["offers"] = map[string]any{
		"name": strings.Repeat("D", 70), "price": 12, "priceCurrency": "USD",
		"availability": strings.Repeat("E", 60) + "SoldOut", "validThrough": "2026-10-02",
	}
	if got := extractStructuredEvidence(structuredTestMarkup(event)); got != "" {
		t.Errorf("oversized record was retained or partially truncated: %q", got)
	}
}

func TestStructuredEvidenceDeduplicatesExactRecordsButNotDifferentOffers(t *testing.T) {
	t.Parallel()
	first := structuredTestEvent("Willow Session")
	first["offers"] = map[string]any{"price": 10, "priceCurrency": "USD"}
	second := structuredTestEvent("Willow Session")
	second["offers"] = map[string]any{"price": 20, "priceCurrency": "USD"}
	got := extractStructuredEvidence(structuredTestMarkup([]any{first, first, second}))
	if strings.Count(got, "Willow Session") != 2 || strings.Count(got, "offer.price=$10") != 1 || strings.Count(got, "offer.price=$20") != 1 {
		t.Errorf("deduplication discarded distinct offers or retained duplicate: %q", got)
	}
}

func TestStructuredEvidenceDollarNormalizationRequiresSameOfferUSD(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name     string
		offer    map[string]any
		parent   map[string]any
		want     []string
		noDollar bool
	}{
		{
			name:  "explicit_usd_number_and_range",
			offer: map[string]any{"price": 18.5, "lowPrice": "12.00", "highPrice": "24", "priceCurrency": "USD"},
			want:  []string{"offer.price=$18.5", "offer.lowPrice=$12.00", "offer.highPrice=$24", "offer.priceCurrency=USD"},
		},
		{
			name:  "missing_currency",
			offer: map[string]any{"price": 18.5, "lowPrice": 12, "highPrice": 24},
			want:  []string{"offer.price=18.5", "offer.lowPrice=12", "offer.highPrice=24"}, noDollar: true,
		},
		{
			name:  "eur_is_not_usd",
			offer: map[string]any{"price": 18.5, "lowPrice": 12, "highPrice": 24, "priceCurrency": "EUR"},
			want:  []string{"offer.price=18.5", "offer.lowPrice=12", "offer.highPrice=24", "offer.priceCurrency=EUR"}, noDollar: true,
		},
		{
			name:  "parent_currency_is_not_offer_currency",
			offer: map[string]any{"price": 18.5}, parent: map[string]any{"priceCurrency": "USD"},
			want: []string{"offer.price=18.5"}, noDollar: true,
		},
		{
			name:  "nonnumeric_price_is_not_rewritten",
			offer: map[string]any{"price": "Call for quote", "priceCurrency": "USD"},
			want:  []string{"offer.price=Call for quote", "offer.priceCurrency=USD"}, noDollar: true,
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			event := structuredTestEvent("Pine Session")
			for key, value := range scenario.parent {
				event[key] = value
			}
			event["offers"] = scenario.offer
			got := extractStructuredEvidence(structuredTestMarkup(event))
			for _, want := range scenario.want {
				if !strings.Contains(got, want) {
					t.Errorf("missing field %q: %q", want, got)
				}
			}
			if scenario.noDollar && strings.Contains(got, "$") {
				t.Errorf("invented USD currency without same-offer numeric USD support: %q", got)
			}
		})
	}
	// Offer siblings may price separate ticket classes in different currencies.
	event := structuredTestEvent("Spruce Session")
	event["offers"] = []any{
		map[string]any{"name": "Dollar offer", "price": 16, "priceCurrency": "USD"},
		map[string]any{"name": "Unspecified offer", "price": 21},
		map[string]any{"name": "Euro offer", "price": 25, "priceCurrency": "EUR"},
	}
	got := extractStructuredEvidence(structuredTestMarkup(event))
	for _, line := range strings.Split(got, "\n") {
		switch {
		case strings.Contains(line, "offer.name=Dollar offer"):
			if !strings.Contains(line, "offer.price=$16") {
				t.Errorf("lost supported same-offer USD normalization: %q", line)
			}
		case strings.Contains(line, "offer.name=Unspecified offer"), strings.Contains(line, "offer.name=Euro offer"):
			if strings.Contains(line, "$") || strings.Contains(line, "USD") {
				t.Errorf("currency crossed sibling offer boundary: %q", line)
			}
		}
	}
	if len(strings.Split(got, "\n")) != 3 {
		t.Errorf("expected all three isolated offers, got %q", got)
	}
}

func TestStructuredEvidenceTraversesKnownContainersOnly(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"@graph", "mainEntity", "itemListElement", "item", "subEvent", "hasMenu", "hasMenuSection", "hasMenuItem"} {
		t.Run(key, func(t *testing.T) {
			got := extractStructuredEvidence(structuredTestMarkup(map[string]any{key: []any{structuredTestEvent("Amber Session")}}))
			if !strings.Contains(got, "Amber Session") {
				t.Errorf("known container %s not traversed: %q", key, got)
			}
		})
	}
	for _, key := range []string{"review", "comment", "isRelatedTo", "publisher", "potentialAction", "untrusted"} {
		t.Run("ignore_"+key, func(t *testing.T) {
			got := extractStructuredEvidence(structuredTestMarkup(map[string]any{key: structuredTestEvent("Foreign Session")}))
			if got != "" {
				t.Errorf("unexpected traversal of %s: %q", key, got)
			}
		})
	}
	first := structuredTestEvent("First Session")
	first["@type"] = []any{"Thing", "https://schema.org/MusicEvent"}
	second := structuredTestEvent("Second Session")
	got := extractStructuredEvidence(structuredTestMarkup([]any{first, map[string]any{"@graph": []any{second}}}))
	if !strings.Contains(got, "First Session") || !strings.Contains(got, "Second Session") {
		t.Errorf("lost top-level arrays, type arrays, or nested graph: %q", got)
	}
}

func TestStructuredEvidenceRejectsInertAndMalformedScripts(t *testing.T) {
	t.Parallel()
	valid := structuredTestMarkup(structuredTestEvent("Hidden Session"))
	for _, scenario := range []struct{ name, markup string }{
		{"comment", "<!--" + valid + "-->"},
		{"ordinary_script", strings.Replace(valid, ` type="application/ld+json"`, "", 1)},
		{"javascript", strings.Replace(valid, "application/ld+json", "application/javascript", 1)},
		{"malformed", `<script type="application/ld+json">{"@type":"Event","name":"Hidden Session",</script>`},
		{"trailing_tokens", strings.Replace(valid, "</script>", ` {"extra":true}</script>`, 1)},
		{"template", "<template>" + valid + "</template>"},
		{"noscript", "<noscript>" + valid + "</noscript>"},
		{"textarea", "<textarea>" + valid + "</textarea>"},
		{"pre", "<pre>" + valid + "</pre>"},
		{"code", "<code>" + valid + "</code>"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if got := extractStructuredEvidence(scenario.markup); got != "" {
				t.Errorf("inactive or invalid data treated as evidence: %q", got)
			}
		})
	}
}

func TestStructuredEvidenceOrdinaryScriptsDoNotConsumeJSONLDBudget(t *testing.T) {
	t.Parallel()
	// Bundled applications commonly precede their actual publisher metadata.
	// Ignoring JS must not discard valid metadata merely because 16 scripts
	// appeared earlier within the already bounded fetched HTML body.
	markup := strings.Repeat(`<script src="/bundle.js"></script>`, 20) + structuredTestMarkup(structuredTestEvent("Visible Session"))
	if got := extractStructuredEvidence(markup); !strings.Contains(got, "Visible Session") {
		t.Errorf("ordinary scripts exhausted JSON-LD extraction budget: %q", got)
	}
}

func TestStructuredEvidenceNeverFetchesRemoteContextsOrIDs(t *testing.T) {
	t.Parallel()
	var remoteCalls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		remoteCalls.Add(1)
		_, _ = io.WriteString(w, `{"name":"Remote content must not be loaded"}`)
	}))
	defer remote.Close()
	event := structuredTestEvent("Local Session")
	event["@context"] = remote.URL + "/context"
	event["location"] = map[string]any{"@id": remote.URL + "/location", "name": "Local Hall"}
	extractor, endpoint := evidenceTestExtractor(t, structuredTestMarkup(event))
	page, err := extractor.fetchPage(context.Background(), endpoint)
	if err != nil || !strings.Contains(page.text, "Local Session") {
		t.Fatalf("local JSON-LD was not extracted: page=%#v err=%v", page, err)
	}
	if remoteCalls.Load() != 0 || strings.Contains(page.text, "Remote content") {
		t.Errorf("remote context or ID was fetched: count=%d page=%q", remoteCalls.Load(), page.text)
	}
}

func TestStructuredEvidenceBoundsRecordsNodesDepthAndScripts(t *testing.T) {
	t.Parallel()
	t.Run("records", func(t *testing.T) {
		var events []any
		for index := 0; index < 40; index++ {
			events = append(events, structuredTestEvent(fmt.Sprintf("Session %02d", index)))
		}
		got := extractStructuredEvidence(structuredTestMarkup(events))
		if lines := strings.Count(got, "\n") + 1; lines > maxStructuredRecords || !strings.Contains(got, "Session 00") {
			t.Errorf("record cap lost or output empty: %d records, %q", lines, got)
		}
	})
	t.Run("nodes", func(t *testing.T) {
		var nodes []any
		for index := 0; index < 300; index++ {
			nodes = append(nodes, map[string]any{"@type": "Thing"})
		}
		nodes = append(nodes, structuredTestEvent("Past Node Budget"))
		if got := extractStructuredEvidence(structuredTestMarkup(nodes)); got != "" {
			t.Errorf("node budget exceeded: %q", got)
		}
	})
	t.Run("depth", func(t *testing.T) {
		var value any = structuredTestEvent("Too Deep")
		for index := 0; index < 12; index++ {
			value = map[string]any{"mainEntity": value}
		}
		if got := extractStructuredEvidence(structuredTestMarkup(value)); got != "" {
			t.Errorf("depth budget exceeded: %q", got)
		}
	})
	t.Run("script_size", func(t *testing.T) {
		event := structuredTestEvent("Oversized Script")
		event["description"] = strings.Repeat("x", 129<<10)
		if got := extractStructuredEvidence(structuredTestMarkup(event)); got != "" {
			t.Errorf("oversized script accepted: %q", got)
		}
	})
	t.Run("script_count", func(t *testing.T) {
		markup := strings.Repeat(`<script type="application/ld+json">{}</script>`, 16) + structuredTestMarkup(structuredTestEvent("Seventeenth Script"))
		if got := extractStructuredEvidence(markup); got != "" {
			t.Errorf("script count budget exceeded: %q", got)
		}
	})
}

func TestStructuredEvidenceNeverDropsOversizedQualifiersFromRetainedClaims(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"eventStatus", "startDate", "endDate", "previousStartDate"} {
		t.Run(key, func(t *testing.T) {
			event := structuredTestEvent("Qualified Session")
			event[key] = strings.Repeat("qualifier ", 20) + "cancelled"
			got := extractStructuredEvidence(structuredTestMarkup(event))
			if got != "" && !strings.Contains(got, "cancelled") {
				t.Errorf("retained event after silently discarding %s qualifier: %q", key, got)
			}
		})
	}
	for _, key := range []string{"availability", "validFrom", "validThrough", "priceValidUntil"} {
		t.Run("offer_"+key, func(t *testing.T) {
			event := structuredTestEvent("Qualified Offer")
			event["offers"] = map[string]any{"price": 8, "priceCurrency": "USD", key: strings.Repeat("qualifier ", 20) + "expired"}
			got := extractStructuredEvidence(structuredTestMarkup(event))
			if strings.Contains(got, "offer.price=$8") && !strings.Contains(got, "expired") {
				t.Errorf("retained price after silently discarding %s qualifier: %q", key, got)
			}
		})
	}
	venue := map[string]any{"@type": "Place", "name": "Qualified Venue", "openingHoursSpecification": map[string]any{"dayOfWeek": []any{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday", "except holidays"}, "opens": "18:00", "closes": "23:00"}}
	if got := extractStructuredEvidence(structuredTestMarkup(venue)); strings.Contains(got, "opens=18:00") && !strings.Contains(got, "except holidays") {
		t.Errorf("retained hours after discarding day qualifiers: %q", got)
	}
}

func TestStructuredEvidenceEnrichmentPreservesAtomicFields(t *testing.T) {
	t.Parallel()
	event := structuredTestEvent("Birch Session")
	event["eventStatus"] = "https://schema.org/EventCancelled"
	event["offers"] = map[string]any{"price": 15, "priceCurrency": "USD", "availability": "https://schema.org/SoldOut"}
	markup := `<h2>Unrelated Gallery</h2><p>Paintings and sculpture.</p>` + structuredTestMarkup(event)
	extractor, endpoint := evidenceTestExtractor(t, markup)
	got, count := extractor.enrich(context.Background(), "Birch Session schedule price", []Result{{URL: endpoint, Snippet: "Birch Session discovery."}})
	excerpts := strings.Join(got[0].PageExcerpts, "\n\n")
	if count != 1 || !strings.Contains(excerpts, "EventCancelled") || !strings.Contains(excerpts, "SoldOut") || !strings.Contains(excerpts, "endDate=2026-10-02T22:00:00-04:00") {
		t.Fatalf("structured fields lost in fetch/enrich/chunks: count=%d result=%#v", count, got)
	}
	for _, block := range got[0].PageExcerpts {
		if strings.Contains(block, "Page structured data:") && strings.Contains(block, "Unrelated Gallery") {
			t.Errorf("structured evidence inherited unrelated HTML section: %q", block)
		}
	}
}

func TestStructuredEvidenceEnrichmentDoesNotCutQualifierAtSnippetLimit(t *testing.T) {
	t.Parallel()
	event := structuredTestEvent("Ash Session")
	event["offers"] = map[string]any{"price": 9, "priceCurrency": "USD", "availability": "https://schema.org/SoldOut"}
	markup := structuredTestMarkup(event)
	full := extractStructuredEvidence(markup)
	if full == "" {
		t.Fatal("fixture must produce structured evidence")
	}
	// The entity and numeric price fit, but the final availability qualifier does not.
	allowance := strings.Index(full, "; offer.availability=")
	if allowance < 0 {
		t.Fatalf("fixture missing terminal offer qualifier: %q", full)
	}
	discovery := strings.Repeat("界", maxSnippetLength-allowance-2)
	extractor, endpoint := evidenceTestExtractor(t, markup)
	got, _ := extractor.enrich(context.Background(), "Ash Session price", []Result{{URL: endpoint, Snippet: discovery}})
	snippet := got[0].Snippet
	if !strings.HasPrefix(snippet, discovery) || !utf8.ValidString(snippet) || utf8.RuneCountInString(snippet) > maxSnippetLength {
		t.Errorf("enrichment corrupted discovery or rune limit: %q", snippet)
	}
	if snippet != discovery || !containsFetchedBlock(got[0].PageExcerpts, full) {
		t.Errorf("discovery must not consume the fetched record's budget or remove SoldOut: %#v", got[0])
	}
}

func TestStructuredEvidenceFetchRetainsExistingURLValidation(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, structuredTestMarkup(structuredTestEvent("Blocked Session")))
	}))
	defer server.Close()
	extractor := &pageExtractor{client: server.Client(), validateURL: func(context.Context, *url.URL) error { return fmt.Errorf("blocked destination") }}
	if _, err := extractor.fetchPage(context.Background(), server.URL); err == nil || calls.Load() != 0 {
		t.Errorf("structured extraction bypassed URL validation: err=%v calls=%d", err, calls.Load())
	}
}
