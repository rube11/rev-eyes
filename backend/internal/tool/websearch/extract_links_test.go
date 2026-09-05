package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type evidenceLinksTransport func(*http.Request) (*http.Response, error)

func (f evidenceLinksTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type evidenceLinksPage struct {
	markup, contentType, redirect string
	status                        int
	err                           error
}

// All requests terminate in memory. The public-target DNS check is replaced
// only to make fixture hostnames deterministic; production redirect scopes,
// HTTP client behavior, content-type checks and budgets remain exercised.
func evidenceLinksFixture(t *testing.T, pages map[string]evidenceLinksPage) (*pageExtractor, func() []string) {
	t.Helper()
	var lock sync.Mutex
	var calls []string
	extractor := newPageExtractor()
	extractor.validateURL = func(context.Context, *url.URL) error { return nil }
	extractor.client.Transport = evidenceLinksTransport(func(request *http.Request) (*http.Response, error) {
		lock.Lock()
		calls = append(calls, request.URL.String())
		lock.Unlock()
		if request.Method != http.MethodGet || request.Body != nil || request.Header.Get("Authorization") != "" || request.Header.Get("X-API-Key") != "" {
			t.Errorf("linked page request was not a credential-free GET: method=%s", request.Method)
		}
		page, exists := pages[request.URL.String()]
		if !exists {
			t.Errorf("unexpected page request outside fixture: %s", request.URL)
			return nil, errors.New("network is forbidden")
		}
		if page.err != nil {
			return nil, page.err
		}
		status := page.status
		if status == 0 {
			status = http.StatusOK
		}
		contentType := page.contentType
		if contentType == "" {
			contentType = "text/html; charset=utf-8"
		}
		header := http.Header{"Content-Type": []string{contentType}}
		if page.redirect != "" {
			status = http.StatusFound
			header.Set("Location", page.redirect)
		}
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(page.markup)), Request: request}, nil
	})
	return extractor, func() []string {
		lock.Lock()
		defer lock.Unlock()
		return append([]string(nil), calls...)
	}
}

func TestDiscoverEvidenceLinksRequiresIntentAndOnlyUsesExplicitAnchors(t *testing.T) {
	t.Parallel()
	base, _ := url.Parse("https://aurora.example/visit/index.html")
	markup := `<nav><a href="../menu&#45;prices"><span>Menu</span> &amp; Prices</a><a href="/hours">Opening hours</a></nav><p>More information is available.</p>`
	links := discoverEvidenceLinks("Aurora menu pricing", base, markup)
	if len(links) != 1 || links[0].url != "https://aurora.example/menu-prices" || links[0].label != "Menu & Prices" {
		t.Errorf("explicit escaped/nav menu anchor not resolved correctly: %#v", links)
	}
	links = discoverEvidenceLinks("Aurora evening schedule hours", base, markup)
	if len(links) != 1 || links[0].url != "https://aurora.example/hours" {
		t.Errorf("schedule intent did not select the hours link: %#v", links)
	}
	for _, query := range []string{"Aurora telescope aperture", "", "latest telescope observations"} {
		if links := discoverEvidenceLinks(query, base, markup); len(links) != 0 {
			t.Errorf("unrelated query followed links: %q %#v", query, links)
		}
	}
	for _, markup := range []string{`<p>Our menu is online.</p>`, `<button onclick="location='/menu'">Menu</button>`, `<a data-href="/menu">Menu</a>`, `<link href="/menu" title="Menu">`} {
		if links := discoverEvidenceLinks("Aurora menu prices", base, markup); len(links) != 0 {
			t.Errorf("invented path or non-href navigation was followed: %#v", links)
		}
	}
	if got := discoverEvidenceLinks("Aurora menu", nil, markup); len(got) != 0 {
		t.Errorf("nil base yielded links: %#v", got)
	}
}

func TestDiscoverEvidenceLinksShowPricesPreferPerformancesWithoutConferenceOrFoodBleed(t *testing.T) {
	t.Parallel()
	base, _ := url.Parse("https://aurora.example/venue")
	markup := `<nav>
<a href="/events">Events</a>
<a href="/meetings">Events &amp; Meetings</a>
<a href="/weddings">Wedding Events</a>
<a href="/conference">Conference Schedule</a>
<a href="/catering">Catering Events</a>
<a href="/restaurant-menu">Restaurant Menu &amp; Prices</a>
<a href="/shows">Shows</a>
<a href="/concerts">Concerts</a>
<a href="/comedy">Comedy</a>
<a href="/performances">Performances</a>
</nav>`
	want := []string{"https://aurora.example/shows", "https://aurora.example/concerts", "https://aurora.example/comedy", "https://aurora.example/performances"}
	for _, query := range []string{"Aurora tonight schedule and ticket prices", "Aurora shows ticket prices", "Aurora comedy performance prices", "Aurora concerts budget"} {
		t.Run(query, func(t *testing.T) {
			var got []string
			for _, link := range discoverEvidenceLinks(query, base, markup) {
				got = append(got, link.url)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("show-price intent followed food/private pages or failed to prefer performance anchors: %#v", got)
			}
		})
	}
}

func TestDiscoverEvidenceLinksExplicitConferenceAndDinnerIntentRemainSupported(t *testing.T) {
	t.Parallel()
	base, _ := url.Parse("https://aurora.example/venue")
	for _, scenario := range []struct{ name, query, markup, want string }{
		{"conference", "Aurora conference schedule", `<a href="/conference-details">Conference Schedule</a>`, "https://aurora.example/conference-details"},
		{"meetings", "Aurora meetings events schedule", `<a href="/meeting-details">Events &amp; Meetings</a>`, "https://aurora.example/meeting-details"},
		{"dinner_with_show", "Aurora dinner menu prices and show schedule", `<a href="/dinner-menu">Restaurant Dinner Menu &amp; Prices</a>`, "https://aurora.example/dinner-menu"},
		{"conference_dinner", "Aurora conference dinner menu prices and schedule", `<a href="/conference-dinner">Conference Dinner Menu</a>`, "https://aurora.example/conference-dinner"},
		{"restaurant_hours", "Aurora restaurant hours and prices", `<a href="/restaurant-menu">Restaurant Menu &amp; Prices</a>`, "https://aurora.example/restaurant-menu"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			got := discoverEvidenceLinks(scenario.query, base, scenario.markup)
			if len(got) != 1 || got[0].url != scenario.want {
				t.Errorf("explicit requested intent was lost: %#v", got)
			}
		})
	}
}

func TestEnrichLinkedShowPricesSpendsBudgetOnShowsNotMenusOrPrivateEvents(t *testing.T) {
	t.Parallel()
	const origin = "https://aurora.example"
	extractor, calls := evidenceLinksFixture(t, map[string]evidenceLinksPage{
		origin + "/venue":    {markup: `<nav><a href="/meetings">Events &amp; Meetings</a><a href="/dining">Restaurant Menu &amp; Prices</a><a href="/events">Events</a><a href="/concerts">Concerts</a><a href="/shows">Shows</a></nav>`},
		origin + "/concerts": {markup: `<p>Aurora concert schedule: the evening performance starts at 11pm and tickets cost $32.</p>`},
		origin + "/shows":    {markup: `<p>Aurora show schedule: the late comedy performance starts at 10:30pm and tickets cost $28.</p>`},
	})
	got, _ := extractor.enrich(context.Background(), "Aurora tonight schedule and ticket prices", []Result{{URL: origin + "/venue", Snippet: "Aurora entertainment discovery."}})
	if len(got) != 3 || len(calls()) != 3 {
		t.Fatalf("show lookup did not use exactly two bounded child attempts: results=%#v calls=%#v", got, calls())
	}
	if got[1].URL != origin+"/concerts" || got[2].URL != origin+"/shows" || got[0].Snippet != "Aurora entertainment discovery." {
		t.Errorf("private/restaurant page consumed the show evidence budget or overwrote parent: %#v", got)
	}
}

func TestDiscoverEvidenceLinksRejectsUnsafeOriginsActionsAndInertMarkup(t *testing.T) {
	t.Parallel()
	base, _ := url.Parse("https://aurora.example:8443/visit")
	for _, scenario := range []struct{ name, markup string }{
		{"external_host", `<a href="https://elsewhere.example:8443/menu">Menu</a>`},
		{"host_suffix", `<a href="https://aurora.example.evil.test:8443/menu">Menu</a>`},
		{"subdomain", `<a href="https://shop.aurora.example:8443/menu">Menu</a>`},
		{"alternate_port", `<a href="https://aurora.example:9443/menu">Menu</a>`},
		{"default_port_changed", `<a href="https://aurora.example/menu">Menu</a>`},
		{"downgrade", `<a href="http://aurora.example:8443/menu">Menu</a>`},
		{"userinfo", `<a href="https://name:fake@aurora.example:8443/menu">Menu</a>`},
		{"javascript", `<a href="javascript:showMenu()">Menu</a>`},
		{"query", `<a href="/menu?language=en">Menu</a>`},
		{"empty_query", `<a href="/menu?">Menu</a>`},
		{"fragment_only", `<a href="#menu">Menu</a>`},
		{"action_path", `<a href="/order/menu">Menu</a>`},
		{"escaped_action_path", `<a href="/%6frder/menu">Menu</a>`},
		{"action_label", `<a href="/menu">B&#111;ok a table and view menu</a>`},
		{"cart_path", `<a href="/cart/menu">Menu</a>`},
		{"login_path", `<a href="/login/menu">Menu</a>`},
		{"pdf", `<a href="/menu.PDF">Menu</a>`},
		{"image", `<a href="/menu.png">Menu</a>`},
		{"download_attribute", `<a href="/menu" download>Menu</a>`},
		{"nonhtml_type", `<a href="/menu-file" type="application/pdf">Menu</a>`},
		{"comment", `<!-- <a href="/menu">Menu</a> -->`},
		{"script", `<script>const markup = '<a href="/menu">Menu</a>';</script>`},
		{"template", `<template><a href="/menu">Menu</a></template>`},
		{"textarea", `<textarea><a href="/menu">Menu</a></textarea>`},
		{"code", `<code><a href="/menu">Menu</a></code>`},
		{"svg", `<svg><a href="/menu">Menu</a></svg>`},
		{"form", `<form action="/checkout"><a href="/menu">Menu</a></form>`},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if got := discoverEvidenceLinks("Aurora menu prices", base, scenario.markup); len(got) != 0 {
				t.Errorf("unsafe/inert candidate discovered: %#v", got)
			}
		})
	}
	if got := discoverEvidenceLinks("Aurora menu", base, `<a href="https://AURORA.EXAMPLE:8443/menu">Menu</a>`); len(got) != 1 {
		t.Errorf("hostname case should not reject the same origin: %#v", got)
	}
}

func TestDiscoverEvidenceLinksCapsCandidatesDeduplicatesAndBoundsAnchorScan(t *testing.T) {
	t.Parallel()
	base, _ := url.Parse("https://aurora.example/visit")
	markup := `<a href="/menu-0">Menu</a><a href="/menu-0#dinner">Menu</a>`
	for index := 1; index < 7; index++ {
		markup += fmt.Sprintf(`<a href="/menu-%d">Menu</a>`, index)
	}
	got := discoverEvidenceLinks("Aurora menu", base, markup)
	if len(got) != 4 {
		t.Fatalf("candidate count=%d, want four", len(got))
	}
	for index, link := range got {
		if link.url != fmt.Sprintf("https://aurora.example/menu-%d", index) {
			t.Errorf("duplicate or unstable candidate selection: %#v", got)
		}
	}
	tooLate := strings.Repeat(`<a href="/about">About</a>`, 512) + `<a href="/menu">Menu</a>`
	if got := discoverEvidenceLinks("Aurora menu", base, tooLate); len(got) != 0 {
		t.Errorf("anchor scan exceeded its 512-anchor bound: %#v", got)
	}
}

func TestDiscoverEvidenceLinksRespectsFirstBaseHrefWithoutCrossOriginResolution(t *testing.T) {
	t.Parallel()
	base, _ := url.Parse("https://aurora.example/visit/index.html")
	for _, scenario := range []struct {
		name, markup string
		want         []string
	}{
		{"same_origin_relative_base", `<head><base href="/menus/"></head><a href="dinner">Menu</a>`, []string{"https://aurora.example/menus/dinner"}},
		{"escaped_base", `<base href="/menu&#115;/"><a href="dinner">Menu</a>`, []string{"https://aurora.example/menus/dinner"}},
		{"first_href_wins", `<base href="/menus/"><base href="/wrong/"><a href="dinner">Menu</a>`, []string{"https://aurora.example/menus/dinner"}},
		{"no_href_base_does_not_win", `<base target="_blank"><base href="/menus/"><a href="dinner">Menu</a>`, []string{"https://aurora.example/menus/dinner"}},
		{"external_base_blocks_relatives", `<base href="https://external.example/menus/"><a href="dinner">Menu</a><a href="https://aurora.example/own-menu">Menu</a>`, []string{"https://aurora.example/own-menu"}},
		{"invalid_base_blocks_relatives", `<base href="https://[bad/"><a href="dinner">Menu</a><a href="https://aurora.example/own-menu">Menu</a>`, []string{"https://aurora.example/own-menu"}},
		{"first_invalid_base_not_repaired", `<base href="javascript:menu()"><base href="/menus/"><a href="dinner">Menu</a>`, nil},
		{"www_is_not_an_alias", `<base href="https://www.aurora.example/menus/"><a href="dinner">Menu</a><a href="https://aurora.example/own-menu">Menu</a>`, []string{"https://aurora.example/own-menu"}},
		{"commented_base_ignored", `<!-- <base href="https://external.example/"> --><a href="menu">Menu</a>`, []string{"https://aurora.example/visit/menu"}},
		{"inert_base_ignored", `<template><base href="https://external.example/"></template><a href="menu">Menu</a>`, []string{"https://aurora.example/visit/menu"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			links := discoverEvidenceLinks("Aurora menu prices", base, scenario.markup)
			var got []string
			for _, link := range links {
				got = append(got, link.url)
			}
			if !reflect.DeepEqual(got, scenario.want) {
				t.Errorf("base href resolution=%#v, want %#v", got, scenario.want)
			}
		})
	}
}

func TestEnrichLinkedEvidenceRetainsParentsUsesChildURLsAndNeverRecurses(t *testing.T) {
	t.Parallel()
	const origin = "https://aurora.example"
	pages := map[string]evidenceLinksPage{
		origin + "/cafe-a": {markup: `<nav><a href="/menu-a">Menu</a><a href="/menu-a-extra">Menu</a></nav>`},
		origin + "/cafe-b": {markup: `<nav><a href="/menu-b">Menu</a><a href="/menu-b-extra">Menu</a></nav>`},
		origin + "/menu-a": {markup: `<meta itemprop="datePublished" content="2026-09-04"><p>Aurora cafe A menu: noodles cost $18 and soup costs $14 per bowl.</p><nav><a href="/deep-menu">Menu</a></nav>`},
		origin + "/menu-b": {markup: `<p>Aurora cafe B menu: dumplings cost $12 and rice costs $9 per plate.</p>`},
	}
	extractor, calls := evidenceLinksFixture(t, pages)
	input := []Result{{URL: origin + "/cafe-a", Title: "Aurora cafe A", Snippet: "Aurora cafe A discovery."}, {URL: origin + "/cafe-b", Title: "Aurora cafe B", Snippet: "Aurora cafe B discovery."}}
	before := append([]Result(nil), input...)
	got, extracted := extractor.enrich(context.Background(), "Aurora cafe menu prices", input)
	if len(got) != 4 || extracted != 2 {
		t.Fatalf("results=%d extracted=%d, want two retained parents plus two child sources", len(got), extracted)
	}
	if !reflect.DeepEqual(input, before) {
		t.Error("linked enrichment mutated caller discovery results")
	}
	for index := range input {
		if got[index].URL != input[index].URL || got[index].Snippet != input[index].Snippet || got[index].PublishedDate != "" || got[index].DiscoveredFrom != "" {
			t.Errorf("child evidence or metadata was misattributed under a parent: %#v", got[index])
		}
	}
	for _, child := range got[2:] {
		if (child.URL != origin+"/menu-a" && child.URL != origin+"/menu-b") || !strings.Contains(strings.Join(child.PageExcerpts, "\n\n"), "$") || child.Snippet != "" || child.DiscoveredFrom == "" || child.Score != 0 {
			t.Errorf("linked source lacks its own provenance or pretends to be ranked discovery: %#v", child)
		}
		if child.URL == origin+"/menu-a" && (child.PagePublishedDate != "2026-09-04" || child.PublishedDate != "" || child.DiscoveredFrom != origin+"/cafe-a") {
			t.Errorf("child publication/discovery metadata=%#v", child)
		}
	}
	if gotCalls := calls(); len(gotCalls) != 4 {
		t.Errorf("more than two child requests or recursion occurred: %#v", gotCalls)
	}
}

func TestEnrichLinkedEvidenceTwoAttemptBudgetIncludesFailures(t *testing.T) {
	t.Parallel()
	const origin = "https://aurora.example"
	for _, failure := range []evidenceLinksPage{{status: http.StatusServiceUnavailable}, {err: errors.New("fixture timeout")}, {contentType: "application/pdf", markup: "not HTML"}} {
		pages := map[string]evidenceLinksPage{
			origin + "/cafe":      {markup: `<nav><a href="/menu-bad">Menu</a><a href="/menu-good">Menu</a><a href="/menu-never">Menu</a></nav>`},
			origin + "/menu-bad":  failure,
			origin + "/menu-good": {markup: `<p>Aurora cafe menu: noodle bowls cost $18, with rice dishes priced at $15 each.</p>`},
		}
		extractor, calls := evidenceLinksFixture(t, pages)
		input := []Result{{URL: origin + "/cafe", Snippet: "Aurora cafe discovery."}}
		got, _ := extractor.enrich(context.Background(), "Aurora cafe menu prices", input)
		if len(got) != 2 || got[0].Snippet != input[0].Snippet || got[1].URL != origin+"/menu-good" || len(calls()) != 3 {
			t.Errorf("child failure lost discovery, consumed unbounded retries, or poisoned successful evidence: results=%#v calls=%#v", got, calls())
		}
	}
}

func TestEnrichLinkedEvidenceKeepsEachChildTitlePublicationAndBranchSeparate(t *testing.T) {
	t.Parallel()
	const origin = "https://aurora.example"
	for _, southDate := range []string{"", "2026-09-03"} {
		t.Run("south_date_"+southDate, func(t *testing.T) {
			southMeta := ""
			if southDate != "" {
				southMeta = `<meta itemprop="datePublished" content="` + southDate + `">`
			}
			pages := map[string]evidenceLinksPage{
				origin + "/north":      {markup: `<nav><a href="/north-menu">Menu</a></nav>`},
				origin + "/south":      {markup: `<nav><a href="/south-menu">Menu</a></nav>`},
				origin + "/north-menu": {markup: `<head><script>const t='<title>FAKE_SCRIPT_TITLE</title>';</script><title>Aurora North &amp; Garden Menu</title><meta itemprop="datePublished" content="2026-09-04"></head><p>Aurora North cafe menu: NORTH_SIGNATURE noodle bowl costs $18.</p>`},
				origin + "/south-menu": {markup: `<head><title>Aurora South Cafe Menu</title>` + southMeta + `</head><p>Aurora South cafe menu: SOUTH_SIGNATURE dumpling plate costs $12.</p>`},
			}
			extractor, _ := evidenceLinksFixture(t, pages)
			input := []Result{
				{URL: origin + "/north", Title: "North discovery title", Snippet: "Aurora North discovery.", PublishedDate: "2026-09-01"},
				{URL: origin + "/south", Title: "South discovery title", Snippet: "Aurora South discovery.", PublishedDate: "2026-09-02"},
			}
			got, _ := extractor.enrich(context.Background(), "Aurora cafe menu prices", input)
			if len(got) != 4 {
				t.Fatalf("linked result count=%d, want two separate child sources", len(got))
			}
			wantParents := append([]Result(nil), input...)
			for index := range wantParents {
				wantParents[index].DiscoverySnippet = input[index].Snippet
				wantParents[index].ExtractionStatus = extractionUnavailable
			}
			if !reflect.DeepEqual(got[:2], wantParents) {
				t.Errorf("child title/publication/evidence overwrote parent branches: %#v", got[:2])
			}
			for _, child := range got[2:] {
				switch child.URL {
				case origin + "/north-menu":
					if child.Title != "Aurora North & Garden Menu" || child.PagePublishedDate != "2026-09-04" || child.DiscoveredFrom != origin+"/north" || !strings.Contains(strings.Join(child.PageExcerpts, "\n\n"), "NORTH_SIGNATURE") || strings.Contains(strings.Join(child.PageExcerpts, "\n\n"), "SOUTH_SIGNATURE") {
						t.Errorf("north child source mixed title/branch/publication: %#v", child)
					}
				case origin + "/south-menu":
					if child.Title != "Aurora South Cafe Menu" || child.PagePublishedDate != southDate || child.DiscoveredFrom != origin+"/south" || !strings.Contains(strings.Join(child.PageExcerpts, "\n\n"), "SOUTH_SIGNATURE") || strings.Contains(strings.Join(child.PageExcerpts, "\n\n"), "NORTH_SIGNATURE") {
						t.Errorf("south child source borrowed another page's title/publication/evidence: %#v", child)
					}
				default:
					t.Errorf("unrequested child branch: %#v", child)
				}
			}
		})
	}
}

func TestEnrichLinkedEvidenceDeduplicatesSharedLinksAndExistingSources(t *testing.T) {
	t.Parallel()
	const origin = "https://aurora.example"
	pages := map[string]evidenceLinksPage{
		origin + "/cafe-a": {markup: `<nav><a href="/menu">Menu</a></nav>`},
		origin + "/cafe-b": {markup: `<nav><a href="/menu#dinner">Menu</a></nav>`},
		origin + "/menu":   {markup: `<p>Aurora menu shows noodle bowls at $18 and soup bowls at $14.</p>`},
	}
	extractor, calls := evidenceLinksFixture(t, pages)
	got, _ := extractor.enrich(context.Background(), "Aurora menu prices", []Result{{URL: origin + "/cafe-a"}, {URL: origin + "/cafe-b"}})
	if len(got) != 3 || len(calls()) != 3 {
		t.Errorf("shared child link duplicated: results=%#v calls=%#v", got, calls())
	}
	extractor, calls = evidenceLinksFixture(t, pages)
	got, _ = extractor.enrich(context.Background(), "Aurora menu prices", []Result{{URL: origin + "/cafe-a"}, {URL: origin + "/menu"}})
	if len(got) != 2 || len(calls()) != 2 {
		t.Errorf("already-returned source refetched/appended as child: results=%#v calls=%#v", got, calls())
	}
}

func TestEnrichLinkedEvidenceRedirectsCannotEscapeScopeOrActions(t *testing.T) {
	t.Parallel()
	const origin = "https://aurora.example"
	for _, target := range []string{"https://outside.example/menu", "https://aurora.example:9443/menu", "http://aurora.example/menu", origin + "/order/menu", origin + "/menu?checkout=1", "https://api.tavily.com/menu"} {
		t.Run(target, func(t *testing.T) {
			extractor, calls := evidenceLinksFixture(t, map[string]evidenceLinksPage{
				origin + "/cafe": {markup: `<nav><a href="/menu">Menu</a></nav>`},
				origin + "/menu": {redirect: target},
			})
			got, _ := extractor.enrich(context.Background(), "Aurora menu prices", []Result{{URL: origin + "/cafe", Snippet: "Original discovery."}})
			if len(got) != 1 || got[0].Snippet != "Original discovery." || len(calls()) != 2 {
				t.Errorf("child redirect escaped scope or lost parent: results=%#v calls=%#v", got, calls())
			}
		})
	}
}

func TestEnrichLinkedEvidenceAllowsDefaultHTTPUpgradeAndKeepsFinalChildURL(t *testing.T) {
	t.Parallel()
	extractor, calls := evidenceLinksFixture(t, map[string]evidenceLinksPage{
		"http://aurora.example/cafe":  {markup: `<nav><a href="/menu">Menu</a></nav>`},
		"http://aurora.example/menu":  {redirect: "https://aurora.example/menu"},
		"https://aurora.example/menu": {markup: `<p>Aurora menu includes noodle bowls costing $18 and dumplings costing $12.</p>`},
	})
	got, _ := extractor.enrich(context.Background(), "Aurora menu prices", []Result{{URL: "http://aurora.example/cafe", Snippet: "Original discovery."}})
	if len(got) != 2 || got[1].URL != "https://aurora.example/menu" || got[1].DiscoveredFrom != "http://aurora.example/cafe" || len(calls()) != 3 {
		t.Errorf("safe protocol upgrade failed or evidence kept wrong source URL: results=%#v calls=%#v", got, calls())
	}
}

func TestEnrichLinkedEvidenceRetainsTargetValidationAndDomainFilters(t *testing.T) {
	t.Parallel()
	const parent = "https://aurora.example/cafe"
	pages := map[string]evidenceLinksPage{parent: {markup: `<nav><a href="/menu">Menu</a></nav>`}}
	extractor, calls := evidenceLinksFixture(t, pages)
	extractor.validateURL = func(_ context.Context, target *url.URL) error {
		if target.Path == "/menu" {
			return errors.New("private DNS target blocked by fixture validator")
		}
		return nil
	}
	got, _ := extractor.enrich(context.Background(), "Aurora menu prices", []Result{{URL: parent, Snippet: "Discovery."}})
	if len(got) != 1 || len(calls()) != 1 {
		t.Errorf("child bypassed target validation: results=%#v calls=%#v", got, calls())
	}
	extractor, calls = evidenceLinksFixture(t, pages)
	got, _ = extractor.enrich(context.Background(), "Aurora menu prices", []Result{{URL: parent, Snippet: "Discovery."}}, "elsewhere.example")
	if len(got) != 1 || len(calls()) != 0 {
		t.Errorf("extraction escaped include_domains: results=%#v calls=%#v", got, calls())
	}
}

func TestSearXNGLinkedEvidenceChildFailuresCannotEnableTavilyFallback(t *testing.T) {
	t.Parallel()
	configured, err := NewConfigured(Config{Provider: ProviderSearXNG, SearXNGBaseURL: "https://search.example", ResearchExtraction: true, TavilyFallback: false, TavilyAPIKey: ""})
	if err != nil {
		t.Fatal(err)
	}
	searcher, ok := configured.(*searxNGTool)
	if !ok {
		t.Fatalf("SearXNG-only factory constructed a fallback wrapper: %T", configured)
	}
	const parent = "https://aurora.example/cafe"
	searcher.client = &http.Client{Transport: evidenceLinksTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Hostname() != "search.example" || request.Method != http.MethodPost {
			t.Errorf("unexpected provider call: host=%s method=%s", request.URL.Hostname(), request.Method)
			return nil, errors.New("network forbidden")
		}
		body := `{"results":[{"title":"Aurora cafe","url":"https://aurora.example/cafe","content":"Aurora menu discovery.","score":1,"engines":["google"]}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	searcher.extractor, _ = evidenceLinksFixture(t, map[string]evidenceLinksPage{
		parent:                        {markup: `<nav><a href="/menu">Menu</a><a href="https://api.tavily.com/menu">Menu</a></nav>`},
		"https://aurora.example/menu": {err: errors.New("child fixture unavailable")},
	})
	result, err := searcher.Execute(context.Background(), tool.Scope{}, json.RawMessage(`{"query":"Aurora cafe menu prices","mode":"research","topic":"general","recency":"none","include_domains":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload searchResponse
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Provider != ProviderSearXNG || payload.Credits != 0 || len(payload.Results) != 1 || payload.Results[0].URL != parent {
		t.Errorf("child extraction failure changed provider or erased discovery: %#v", payload)
	}
}
