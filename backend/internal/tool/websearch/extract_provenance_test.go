package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func TestFetchedProvenanceSeparatesMisleadingDiscoveryAndPageSections(t *testing.T) {
	const endpoint = "https://cinder.example/visit"
	const discovery = "Cinder Mesa Park is open 9am to 4pm daily."
	extractor, _ := evidenceLinksFixture(t, map[string]evidenceLinksPage{endpoint: {markup: `<main><h1>Cinder Mesa Park</h1><h2>Park grounds</h2><p>The park grounds are open daily from 6am to 10pm.</p><h2>Visitor center</h2><p>The visitor center is open daily from 9am to 4pm.</p></main>`}})
	input := []Result{{URL: endpoint, Title: "Cinder Mesa Park hours", Snippet: discovery}}
	got, _ := extractor.enrich(context.Background(), "Cinder Mesa park grounds visitor center hours", input)
	if len(got) != 1 || got[0].DiscoverySnippet != discovery || got[0].ExtractionStatus != "succeeded" {
		t.Fatalf("discovery/fetched status was lost: %#v", got)
	}
	for _, want := range []string{
		"Section: Cinder Mesa Park > Park grounds\nThe park grounds are open daily from 6am to 10pm.",
		"Section: Cinder Mesa Park > Visitor center\nThe visitor center is open daily from 9am to 4pm.",
	} {
		if !containsFetchedBlock(got[0].PageExcerpts, want) {
			t.Errorf("fetched section ownership was lost: want %q in %#v", want, got[0].PageExcerpts)
		}
	}
	for _, block := range got[0].PageExcerpts {
		if strings.Contains(block, discovery) {
			t.Errorf("misleading discovery acquired fetched-page provenance: %q", block)
		}
	}
	if got[0].Snippet != discovery {
		t.Fatalf("fetched evidence was mixed back into discovery: %q", got[0].Snippet)
	}
	if input[0].DiscoverySnippet != "" || input[0].PageExcerpts != nil || input[0].ExtractionStatus != "" || input[0].Snippet != discovery {
		t.Fatalf("enrichment mutated its discovery input: %#v", input)
	}
}

func TestFetchedProvenanceKeepsIdenticalSchedulesWithDistinctOwners(t *testing.T) {
	const endpoint = "https://cinder.example/entrances"
	extractor, _ := evidenceLinksFixture(t, map[string]evidenceLinksPage{endpoint: {markup: `<main><h1>Cinder Mesa Park</h1><h2>North entrance</h2><p>Open daily from 6am to 10pm.</p><h2>South entrance</h2><p>Open daily from 6am to 10pm.</p></main>`}})
	got, _ := extractor.enrich(context.Background(), "Cinder Mesa north south entrance hours", []Result{{URL: endpoint, Snippet: "Find Cinder Mesa entrance hours."}})
	for _, owner := range []string{"North entrance", "South entrance"} {
		want := "Section: Cinder Mesa Park > " + owner + "\nOpen daily from 6am to 10pm."
		if !containsFetchedBlock(got[0].PageExcerpts, want) {
			t.Errorf("duplicate schedule text lost its separate owner %q: %#v", owner, got[0].PageExcerpts)
		}
	}
}

func TestFetchedProvenanceFailedOrUnusablePageRetainsDiscoveryAsUnavailable(t *testing.T) {
	const endpoint = "https://cinder.example/visit"
	const discovery = "Cinder Mesa visitor information from discovery."
	for _, test := range []struct {
		name string
		page evidenceLinksPage
	}{
		{"transport_error", evidenceLinksPage{err: errors.New("offline fetch failure")}},
		{"missing_page", evidenceLinksPage{status: http.StatusNotFound}},
		{"non_html", evidenceLinksPage{contentType: "application/pdf", markup: "not an HTML page"}},
		{"footer_only", evidenceLinksPage{markup: `<footer>Privacy policy and newsletter registration.</footer>`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			extractor, _ := evidenceLinksFixture(t, map[string]evidenceLinksPage{endpoint: test.page})
			got, _ := extractor.enrich(context.Background(), "Cinder Mesa hours", []Result{{URL: endpoint, Snippet: discovery}})
			if len(got) != 1 || got[0].DiscoverySnippet != discovery || got[0].Snippet != discovery || got[0].ExtractionStatus != "unavailable" || len(got[0].PageExcerpts) != 0 {
				t.Fatalf("failed extraction was omitted or upgraded to fetched evidence: %#v", got)
			}
		})
	}
}

func TestFetchedProvenanceLongDiscoveryCannotConsumeFetchedEvidenceBudget(t *testing.T) {
	const endpoint = "https://cinder.example/visit"
	discovery := strings.Repeat("星", maxSnippetLength)
	markup := `<main><h1>Cinder Mesa Park</h1><h2>Park grounds</h2><p>The park grounds open from 6am to 10pm. ` + strings.Repeat("山", 470) + `</p><h2>Visitor center</h2><p>The visitor center opens from 9am to 4pm. ` + strings.Repeat("川", 470) + `</p></main>`
	extractor, _ := evidenceLinksFixture(t, map[string]evidenceLinksPage{endpoint: {markup: markup}})
	got, _ := extractor.enrich(context.Background(), "Cinder Mesa park grounds visitor center hours", []Result{{URL: endpoint, Snippet: discovery}})
	if got[0].DiscoverySnippet != discovery || got[0].Snippet != discovery || got[0].ExtractionStatus != "succeeded" || len(got[0].PageExcerpts) == 0 {
		t.Fatalf("full discovery suppressed the independent fetched evidence: status=%q excerpts=%#v", got[0].ExtractionStatus, got[0].PageExcerpts)
	}
	fetched := strings.Join(got[0].PageExcerpts, "\n\n")
	if !utf8.ValidString(fetched) || utf8.RuneCountInString(fetched) > maxSnippetLength || !strings.Contains(fetched, "6am to 10pm") || !strings.Contains(fetched, "9am to 4pm") || strings.Contains(fetched, "星") {
		t.Fatalf("fetched evidence exceeded its budget or mixed/lost source facts: %d runes %q", utf8.RuneCountInString(fetched), fetched)
	}
}

func TestFetchedProvenanceSearXNGWireMarksUnrequestedExtraction(t *testing.T) {
	for _, mode := range []string{"quick", "research"} {
		t.Run(mode, func(t *testing.T) {
			searcher, err := newSearXNG("https://searxng.invalid", false)
			if err != nil {
				t.Fatal(err)
			}
			// Extraction is disabled even in research mode; the provider's
			// discovery response is supplied entirely in memory.
			searcher.client.Transport = provenanceSearchResponse(`{"results":[{"title":"Cinder Mesa","url":"https://cinder.example/visit","content":"Discovery-only Cinder Mesa hours.","score":1}]}`)
			arguments, _ := json.Marshal(map[string]any{"query": "Cinder Mesa hours", "mode": mode, "topic": "general", "recency": "none", "include_domains": []string{}})
			result, err := searcher.Execute(context.Background(), tool.Scope{}, arguments)
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				Results []Result `json:"results"`
			}
			if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Results) != 1 || payload.Results[0].ExtractionStatus != "not_requested" || payload.Results[0].DiscoverySnippet != "Discovery-only Cinder Mesa hours." || len(payload.Results[0].PageExcerpts) != 0 {
				t.Fatalf("wire payload misstated extraction provenance: %#v", payload.Results)
			}
		})
	}
}

func TestFetchedProvenanceSearXNGResearchWireRetainsIndependentFields(t *testing.T) {
	const endpoint = "https://cinder.example/visit"
	searcher, err := newSearXNG("https://searxng.invalid", true)
	if err != nil {
		t.Fatal(err)
	}
	searcher.client.Transport = provenanceSearchResponse(`{"results":[{"title":"Cinder Mesa Park","url":"https://cinder.example/visit","content":"Discovery says 9am to 4pm.","score":1,"publishedDate":"2020-01-01T12:00:00Z"}]}`)
	searcher.extractor, _ = evidenceLinksFixture(t, map[string]evidenceLinksPage{endpoint: {markup: `<meta property="article:published_time" content="2026-09-03T16:00:00Z"><main><h1>Cinder Mesa Park</h1><h2>Park grounds</h2><p>The park grounds open daily from 6am to 10pm.</p></main>`}})
	result, err := searcher.Execute(context.Background(), tool.Scope{}, json.RawMessage(`{"query":"Cinder Mesa park hours","mode":"research","topic":"general","recency":"none","include_domains":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Results []struct {
			Discovery string   `json:"discovery_snippet"`
			Excerpts  []string `json:"page_excerpts"`
			Status    string   `json:"extraction_status"`
			PageDate  string   `json:"page_published_date"`
			OldDate   string   `json:"published_date"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Results) != 1 {
		t.Fatalf("unexpected research result count: %s", result.Content)
	}
	row := payload.Results[0]
	if row.Discovery != "Discovery says 9am to 4pm." || row.Status != "succeeded" || row.PageDate != "2026-09-03T16:00:00Z" || row.OldDate != "2020-01-01T12:00:00Z" || !containsFetchedBlock(row.Excerpts, "Section: Cinder Mesa Park > Park grounds\nThe park grounds open daily from 6am to 10pm.") {
		t.Fatalf("research JSON field contract lost provenance: %#v", row)
	}
}

func TestFetchedProvenanceLinkedPageHasItsOwnEvidenceAndNoDiscovery(t *testing.T) {
	const parent, child = "https://aurora.example/", "https://aurora.example/menu"
	extractor, calls := evidenceLinksFixture(t, map[string]evidenceLinksPage{
		parent: {markup: `<nav><a href="/menu">Dinner menu</a></nav>`},
		child:  {markup: `<title>Aurora dinner menu</title><main><h1>Aurora dinner</h1><p>The vegetable dinner costs $18 per person.</p></main>`},
	})
	got, _ := extractor.enrich(context.Background(), "Aurora dinner menu prices", []Result{{URL: parent, Snippet: "Aurora restaurant discovery."}})
	if len(got) != 2 || !reflect.DeepEqual(calls(), []string{parent, child}) {
		t.Fatalf("linked fixture did not fetch one parent and child: %#v calls=%v", got, calls())
	}
	if got[0].ExtractionStatus != "unavailable" || len(got[0].PageExcerpts) != 0 || strings.Contains(got[0].Snippet, "$18") {
		t.Fatalf("linked child evidence was attributed to navigation-only parent: %#v", got[0])
	}
	if got[1].URL != child || got[1].DiscoveredFrom != parent || got[1].DiscoverySnippet != "" || got[1].ExtractionStatus != "succeeded" || !strings.Contains(strings.Join(got[1].PageExcerpts, "\n\n"), "$18") {
		t.Fatalf("linked child lost independent fetched provenance: %#v", got[1])
	}
}

func TestFetchedProvenancePublicationMetadataNeverInheritsDiscoveryDate(t *testing.T) {
	const endpoint = "https://cinder.example/news"
	for _, test := range []struct{ name, metadata, pageDate string }{
		{"explicit_page_date", `<meta property="article:published_time" content="2026-09-03T16:00:00Z">`, "2026-09-03T16:00:00Z"},
		{"page_date_missing", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			extractor, _ := evidenceLinksFixture(t, map[string]evidenceLinksPage{endpoint: {markup: test.metadata + `<main><h1>Cinder Mesa news</h1><p>Cinder Mesa opened a new viewing platform on Thursday.</p></main>`}})
			got, _ := extractor.enrich(context.Background(), "Cinder Mesa viewing platform", []Result{{URL: endpoint, Snippet: "Cinder Mesa discovery lead.", PublishedDate: "2020-01-01T12:00:00Z"}})
			if got[0].ExtractionStatus != "succeeded" || got[0].PublishedDate != "2020-01-01T12:00:00Z" || got[0].PagePublishedDate != test.pageDate {
				t.Fatalf("fetched metadata inherited/overwrote discovery publication: %#v", got[0])
			}
		})
	}
}

func containsFetchedBlock(blocks []string, want string) bool {
	for _, block := range blocks {
		if block == want {
			return true
		}
	}
	return false
}

func provenanceSearchResponse(body string) http.RoundTripper {
	return evidenceLinksTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
}
