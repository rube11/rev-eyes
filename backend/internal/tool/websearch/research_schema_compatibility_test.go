package websearch

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func TestResearchOnlyModelSpecPreservesDirectQuickAndBackgroundNews(t *testing.T) {
	t.Parallel()
	const endpoint = "https://cinder.example/visit"
	searcher, err := newSearXNG("https://searxng.invalid", true)
	if err != nil {
		t.Fatal(err)
	}
	searcher.client.Transport = provenanceSearchResponse(`{"results":[{"title":"Cinder Mesa Park","url":"https://cinder.example/visit","content":"Cinder Mesa opening hours.","score":1}]}`)
	extractor, fetchedURLs := evidenceLinksFixture(t, map[string]evidenceLinksPage{
		endpoint: {markup: `<main><h1>Cinder Mesa Park</h1><p>The park opens daily from 6am to 10pm.</p></main>`},
	})
	searcher.extractor = extractor
	// The model-facing schema is deliberately narrower than direct execution.
	for _, mode := range []string{"quick", "research"} {
		arguments, _ := json.Marshal(map[string]any{"query": "Cinder Mesa park hours", "mode": mode, "topic": "general", "recency": "none", "include_domains": []string{}})
		result, err := searcher.Execute(context.Background(), tool.Scope{}, arguments)
		if err != nil {
			t.Fatal(err)
		}
		var response searchResponse
		if err := json.Unmarshal([]byte(result.Content), &response); err != nil {
			t.Fatal(err)
		}
		if response.Mode != mode || len(response.Results) != 1 {
			t.Fatalf("direct mode changed: %#v", response)
		}
		row := response.Results[0]
		if mode == "quick" {
			if len(fetchedURLs()) != 0 || len(row.PageExcerpts) != 0 || row.ExtractionStatus != extractionNotRequested {
				t.Fatalf("direct quick unexpectedly fetched source pages: row=%#v fetched=%v", row, fetchedURLs())
			}
		} else if len(fetchedURLs()) != 1 || len(row.PageExcerpts) == 0 || row.ExtractionStatus != extractionSucceeded {
			t.Fatalf("research lost fetched evidence: row=%#v fetched=%v", row, fetchedURLs())
		}
	}
	results, err := searcher.SearchNews(context.Background(), "Cinder Mesa announcement")
	if err != nil || len(results) != 1 || len(fetchedURLs()) != 1 || len(results[0].PageExcerpts) != 0 {
		t.Fatalf("background news changed retrieval behavior: results=%#v error=%v fetched=%v", results, err, fetchedURLs())
	}
}
