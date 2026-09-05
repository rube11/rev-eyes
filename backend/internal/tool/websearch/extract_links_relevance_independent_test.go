package websearch

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestIndependentEvidenceLinkRelevanceOpeningHoursDoNotImplyEvents(t *testing.T) {
	base, _ := url.Parse("https://harbor.example/visit")
	markup := `<nav><a href="/events">Events</a><a href="/shows">Shows</a><a href="/concerts">Concerts</a><a href="/calendar">Calendar</a><a href="/hours">Opening Hours</a><a href="/dinner">Dinner Menu</a></nav>`
	for _, query := range []string{
		"Harbor park day-use opening hours",
		"Harbor admission price closing hours",
		"Harbor restaurant opening hours tonight",
		"Harbor restaurant dinner menu and opening hours",
	} {
		t.Run(query, func(t *testing.T) {
			links := discoverEvidenceLinks(query, base, markup)
			foundHours := false
			for _, link := range links {
				if link.url == "https://harbor.example/hours" {
					foundHours = true
				}
				if strings.Contains(link.url, "/events") || strings.Contains(link.url, "/shows") || strings.Contains(link.url, "/concerts") || strings.Contains(link.url, "/calendar") {
					t.Errorf("opening-hours intent acquired unrequested event navigation: %#v", links)
				}
			}
			if !foundHours {
				t.Errorf("specific opening-hours evidence was lost: %#v", links)
			}
		})
	}
	for _, query := range []string{"Harbor opening hours and events", "Harbor shows and opening hours", "Harbor concert schedule"} {
		links := discoverEvidenceLinks(query, base, markup)
		foundEvent := false
		for _, link := range links {
			if strings.Contains(link.url, "/events") || strings.Contains(link.url, "/shows") || strings.Contains(link.url, "/concerts") || strings.Contains(link.url, "/calendar") {
				foundEvent = true
			}
		}
		if !foundEvent {
			t.Errorf("explicit event or mixed intent was over-filtered for %q: %#v", query, links)
		}
	}
}

func TestIndependentEvidenceLinkRelevanceCapturedHoursNavigation(t *testing.T) {
	raw := readWebResearchTestCorpus(t, "web-search-fee-ownership-capture-2026-09-04.json")
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
		if page.URL != "https://parks.nv.gov/fees" {
			continue
		}
		base, err := url.Parse(page.URL)
		if err != nil {
			t.Fatal(err)
		}
		// This is the preserved raw source that supplied unrelated statewide
		// events during the live8 day-use-hours search, not a recreated page.
		if got := discoverEvidenceLinks("Valley of Fire State Park day-use hours entrance fee non-Nevada car", base, page.Markup); len(got) != 0 {
			t.Errorf("captured hours-only query still spends links on statewide event navigation: %#v", got)
		}
		found := false
		for _, link := range discoverEvidenceLinks("Nevada State Parks events calendar", base, page.Markup) {
			if strings.TrimSuffix(link.url, "/") == "https://parks.nv.gov/events" {
				found = true
			}
		}
		if !found {
			t.Error("the same captured explicit events anchors were lost for an actual events query")
		}
		return
	}
	t.Fatal("expected fee page capture is absent")
}

func TestIndependentEvidenceLinkRelevanceRequestedMenuServiceWins(t *testing.T) {
	base, _ := url.Parse("https://harbor.example/restaurant")
	// Mismatching pages precede the useful one, so passing requires intent
	// relevance rather than relying on the original navigation order.
	for _, scenario := range []struct {
		name, query, markup, want string
	}{
		{"dinner", "Harbor dinner menu prices", `<a href="/breakfast">Breakfast Menu</a><a href="/dessert">Dessert Menu</a><a href="/catering">Catering Menu and Prices</a><a href="/menu">Menu</a><a href="/dinner">Dinner Menu</a>`, "/dinner"},
		{"breakfast", "Harbor breakfast menu prices", `<a href="/dinner">Dinner Menu</a><a href="/dessert">Dessert Menu</a><a href="/catering">Catering Menu and Prices</a><a href="/menu">Menu</a><a href="/breakfast">Breakfast Menu</a>`, "/breakfast"},
		{"lunch", "Harbor lunch menu prices", `<a href="/dinner">Dinner Menu</a><a href="/breakfast">Breakfast Menu</a><a href="/catering">Catering Menu and Prices</a><a href="/menu">Menu</a><a href="/lunch">Lunch Menu</a>`, "/lunch"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			links := discoverEvidenceLinks(scenario.query, base, scenario.markup)
			if len(links) == 0 || links[0].url != "https://harbor.example"+scenario.want {
				t.Errorf("requested service lost to generic or mismatching menu navigation: %#v", links)
			}
			for _, link := range links {
				if link.url == "https://harbor.example/catering" {
					t.Errorf("unrequested catering page remained eligible for a normal meal: %#v", links)
				}
			}
		})
	}
	for _, query := range []string{"Harbor catering menu prices", "Harbor conference dinner catering menu"} {
		links := discoverEvidenceLinks(query, base, `<a href="/catering">Catering Menu and Prices</a>`)
		if len(links) != 1 || links[0].url != "https://harbor.example/catering" {
			t.Errorf("explicit catering request was blocked: query=%q links=%#v", query, links)
		}
	}
}

func TestIndependentEvidenceLinkRelevanceChildBudgetUsesRequestedMeals(t *testing.T) {
	const origin = "https://harbor.example"
	pages := map[string]evidenceLinksPage{
		origin + "/restaurant": {markup: `<a href="/catering">Catering Menu and Prices</a><a href="/dessert">Dessert Menu</a><a href="/breakfast">Breakfast Menu</a><a href="/dinner">Dinner Menu</a><a href="/menu">Menu</a>`},
		origin + "/catering":   {markup: `<p>Catering menu: a tray for twenty guests costs $180.</p>`},
		origin + "/dessert":    {markup: `<p>Dessert menu: a single chocolate cake slice costs $8.</p>`},
		origin + "/breakfast":  {markup: `<p>Breakfast menu: eggs and toast cost $12 per plate.</p>`},
		origin + "/dinner":     {markup: `<p>Dinner menu: the Garden Bowl costs $18 per person.</p>`},
		origin + "/menu":       {markup: `<p>Harbor all-day menu: the Garden Plate costs $17 per person.</p>`},
	}
	extractor, calls := evidenceLinksFixture(t, pages)
	got, _ := extractor.enrich(context.Background(), "Harbor dinner menu prices", []Result{{URL: origin + "/restaurant", Snippet: "Original restaurant discovery."}})
	if len(calls()) != 3 || len(got) != 3 {
		t.Fatalf("expected one retained parent and two bounded child fetches: calls=%#v results=%#v", calls(), got)
	}
	if got[1].URL != origin+"/dinner" || got[2].URL != origin+"/menu" {
		t.Errorf("two-child budget was spent on a mismatching service or catering: calls=%#v results=%#v", calls(), got)
	}
}
