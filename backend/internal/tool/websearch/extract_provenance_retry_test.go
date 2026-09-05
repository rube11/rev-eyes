package websearch

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestFailedReenrichDoesNotRelabelFetchedOnlyParentAsDiscovery(t *testing.T) {
	t.Parallel()
	const endpoint = "https://juniper.example/visit"
	const excerpt = "Section: Juniper Gardens > Visitor center\nThe visitor center is open from 10am to 3pm."
	extractor, calls := evidenceLinksFixture(t, map[string]evidenceLinksPage{
		endpoint: {err: errors.New("offline re-fetch failure")},
	})
	input := []Result{{
		URL: endpoint, Title: "Juniper Gardens", Snippet: excerpt,
		PageExcerpts: []string{excerpt}, PagePublishedDate: "2026-09-01",
		ExtractionStatus: extractionSucceeded,
	}}
	wantInput := append([]Result(nil), input...)
	wantInput[0].PageExcerpts = append([]string(nil), input[0].PageExcerpts...)

	got, extracted := extractor.enrich(context.Background(), "Juniper Gardens visitor center hours", input)
	if len(got) != 1 || extracted != 0 || len(calls()) != 1 {
		t.Fatalf("failed re-fetch result count=%d extracted=%d calls=%#v", len(got), extracted, calls())
	}
	if got[0].DiscoverySnippet != "" || got[0].ExtractionStatus != extractionUnavailable ||
		len(got[0].PageExcerpts) != 0 || got[0].PagePublishedDate != "" {
		t.Errorf("failed re-fetch relabeled or retained stale fetched provenance: %#v", got[0])
	}
	if got[0].Snippet != "" {
		t.Errorf("failed re-fetch retained stale combined evidence: %q", got[0].Snippet)
	}
	if !reflect.DeepEqual(input, wantInput) {
		t.Errorf("re-fetch mutated its input or excerpt backing slice: got=%#v want=%#v", input, wantInput)
	}
}
