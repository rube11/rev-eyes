package openai

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const referenceRegistryURL = "https://observatory.example/visiting"

func referenceRegistry(t *testing.T, sources map[string][]webEvidenceSource) *webEvidenceReferences {
	t.Helper()
	refs, err := newWebEvidenceReferences()
	if err != nil {
		t.Fatal(err)
	}
	refs.sync(sources)
	return refs
}

func referenceRegistryJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func referenceRegistryOutput(t *testing.T, provider string, rows any) json.RawMessage {
	t.Helper()
	content := referenceRegistryJSON(t, map[string]any{"provider": provider, "results": rows, "diagnostic": "preserve payload"})
	return referenceRegistryJSON(t, map[string]any{"type": "function_call_output", "call_id": "call-1", "output": string(content), "extra": "preserve envelope"})
}

func referenceRegistryRows(t *testing.T, raw json.RawMessage) []map[string]json.RawMessage {
	t.Helper()
	var envelope toolOutput
	var payload struct {
		Results []map[string]json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(envelope.Output), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Results
}

func TestWebEvidenceReferencesRowsKeepIndependentMetadataAndInvalidateChangedIdentity(t *testing.T) {
	const passage = "The telescope opened Thursday for public viewing."
	sources := map[string][]webEvidenceSource{referenceRegistryURL: {
		{URL: referenceRegistryURL, Title: "First capture", PageExcerpts: []string{passage}, ExtractionStatus: "succeeded", PagePublishedDate: "2026-09-03", PublishedDate: "1999-01-01"},
		{URL: referenceRegistryURL, Title: "Second capture", PageExcerpts: []string{passage}, ExtractionStatus: "succeeded", PagePublishedDate: "2026-08-27", PublishedDate: "1999-01-01"},
	}}
	refs := referenceRegistry(t, sources)
	first := refs.snapshot()
	if len(first) != 2 || first[0].ID == first[1].ID || first[0].Source.PagePublishedDate == first[1].Source.PagePublishedDate {
		t.Fatalf("same text collapsed independent row metadata: %#v", first)
	}
	refs.sync(sources)
	if !reflect.DeepEqual(first, refs.snapshot()) {
		t.Fatal("unchanged rows did not preserve stable IDs and reproducible snapshots")
	}
	oldStamp := sources[referenceRegistryURL][0].referenceRowID
	oldID := referenceBlockID(oldStamp, 0)
	sources[referenceRegistryURL][0].PagePublishedDate = "2026-09-04"
	refs.sync(sources)
	if _, found := refs.resolve(oldID); found || sources[referenceRegistryURL][0].referenceRowID == oldStamp {
		t.Fatal("changed metadata silently rebound a previously issued reference")
	}
	retained := sources[referenceRegistryURL][0]
	latestID := referenceBlockID(retained.referenceRowID, 0)
	refs.sync(nil)
	refs.sync(map[string][]webEvidenceSource{referenceRegistryURL: {retained}})
	if _, found := refs.resolve(latestID); found {
		t.Fatal("reinserting an evicted row revived its stale reference")
	}
}

func TestWebEvidenceReferencesDuplicateOrUpstreamRowStampsCannotAlias(t *testing.T) {
	row := webEvidenceSource{URL: referenceRegistryURL, PageExcerpts: []string{"The public telescope is on the upper terrace."}, ExtractionStatus: "succeeded", referenceRowID: "upstream-chosen-id"}
	sources := map[string][]webEvidenceSource{referenceRegistryURL: {row}}
	refs := referenceRegistry(t, sources)
	stamp := sources[referenceRegistryURL][0].referenceRowID
	if stamp == row.referenceRowID {
		t.Fatal("registry trusted an upstream row stamp")
	}
	sources[referenceRegistryURL] = append(sources[referenceRegistryURL], sources[referenceRegistryURL][0])
	refs.sync(sources)
	if got := refs.snapshot(); len(got) != 2 || got[0].ID == got[1].ID || sources[referenceRegistryURL][1].referenceRowID == stamp {
		t.Fatalf("copied private row stamp aliased distinct retained rows: %#v", got)
	}
}

func TestWebEvidenceReferencesWholeBlockLimitsKeepOriginalOrdinals(t *testing.T) {
	const eligible = "The garden remains open only on weekdays."
	tooLong := strings.Repeat("x", 1001)
	boundary := strings.Repeat("é", 1000)
	sources := map[string][]webEvidenceSource{referenceRegistryURL: {{URL: referenceRegistryURL, ExtractionStatus: "succeeded", PageExcerpts: []string{"short", tooLong, eligible, "Fourth block must not expand the registry."}}}}
	refs := referenceRegistry(t, sources)
	snapshot := refs.snapshot()
	if len(snapshot) != 1 || snapshot[0].Source.PageExcerpts[0] != eligible || !strings.HasSuffix(snapshot[0].ID, "b3") {
		t.Fatalf("blocks were truncated, renumbered, or expanded beyond original three slots: %#v", snapshot)
	}
	sources[referenceRegistryURL][0].PageExcerpts = []string{boundary}
	refs.sync(sources)
	if got := refs.snapshot(); len(got) != 1 || got[0].Source.PageExcerpts[0] != boundary {
		t.Fatal("whole Unicode passage at the 1000-character boundary was not retained exactly")
	}
	for _, row := range []webEvidenceSource{
		{URL: referenceRegistryURL, Snippet: eligible},
		{URL: referenceRegistryURL, ExtractionStatus: "succeeded", DiscoverySnippet: eligible},
		{URL: referenceRegistryURL, PageExcerpts: []string{eligible}},
		{URL: referenceRegistryURL, ExtractionStatus: "unavailable", PageExcerpts: []string{eligible}},
		{URL: referenceRegistryURL, ExtractionStatus: "not_requested", PageExcerpts: []string{eligible}},
		{URL: "https://different.example/", ExtractionStatus: "succeeded", PageExcerpts: []string{eligible}},
		{URL: "not a URL", ExtractionStatus: "succeeded", PageExcerpts: []string{eligible}},
	} {
		refs.sync(map[string][]webEvidenceSource{referenceRegistryURL: {row}})
		if len(refs.snapshot()) != 0 {
			t.Errorf("ineligible or mismatched source minted references: %#v", row)
		}
	}
}

func TestWebEvidenceReferencesProjectionPreservesRawOutputAndMatchesFullRows(t *testing.T) {
	first := webEvidenceSource{URL: referenceRegistryURL, Title: "Visiting", Snippet: "Discovery lead", DiscoverySnippet: "Discovery lead", ExtractionStatus: "succeeded", PagePublishedDate: "2026-09-03", PageExcerpts: []string{"The garden opened Thursday for public viewing.", "The telescope operates only on clear evenings."}}
	second := cloneReferenceSource(first)
	second.PagePublishedDate = "2026-08-27"
	unretained := cloneReferenceSource(first)
	unretained.PagePublishedDate = "2026-09-04"
	raw := referenceRegistryOutput(t, "searxng", []webEvidenceSource{first, second, unretained})
	original := append(json.RawMessage(nil), raw...)
	sources := map[string][]webEvidenceSource{referenceRegistryURL: {first, second}}
	refs := referenceRegistry(t, sources)
	projected := refs.project([]toolCall{{Name: "search_web"}}, []json.RawMessage{raw}, sources)
	if len(projected) != 1 || !bytes.Equal(raw, original) || bytes.Equal(projected[0], raw) {
		t.Fatal("projection did not privately transform an independent copy")
	}
	before, after := referenceRegistryRows(t, raw), referenceRegistryRows(t, projected[0])
	for index, row := range after {
		var blocks []struct{ ID, Text string }
		if err := json.Unmarshal(row["page_excerpts"], &blocks); err != nil {
			t.Fatal(err)
		}
		if index < 2 {
			if len(blocks) != 2 {
				t.Fatalf("retained row lost a block: %#v", blocks)
			}
			for blockIndex, block := range blocks {
				bound, found := refs.resolve(block.ID)
				if !found || len(bound.PageExcerpts) != 1 || bound.PageExcerpts[0] != block.Text || block.Text != first.PageExcerpts[blockIndex] || bound.PagePublishedDate != sources[referenceRegistryURL][index].PagePublishedDate {
					t.Fatalf("projected reference rebound another block or capture date: %#v %#v", block, bound)
				}
			}
		} else if len(blocks) != 0 {
			t.Fatal("same URL and text borrowed a retained row's different capture metadata")
		}
		delete(before[index], "page_excerpts")
		delete(row, "page_excerpts")
		if !reflect.DeepEqual(before[index], row) {
			t.Fatalf("projection changed unrelated source contract fields: %#v %#v", before[index], row)
		}
	}
	var envelope map[string]json.RawMessage
	var payload map[string]json.RawMessage
	var content string
	_ = json.Unmarshal(projected[0], &envelope)
	_ = json.Unmarshal(envelope["output"], &content)
	_ = json.Unmarshal([]byte(content), &payload)
	if string(envelope["extra"]) != `"preserve envelope"` || string(payload["diagnostic"]) != `"preserve payload"` {
		t.Fatal("projection discarded unrelated envelope or payload fields")
	}
	projected[0][0] = 'x'
	if !bytes.Equal(raw, original) {
		t.Fatal("projected result aliases raw tool-output storage")
	}
}

func TestWebEvidenceReferencesProjectionCannotImportReferencesOrBypassSync(t *testing.T) {
	row := webEvidenceSource{URL: referenceRegistryURL, ExtractionStatus: "succeeded", PageExcerpts: []string{"The telescope operates only on clear evenings."}}
	sources := map[string][]webEvidenceSource{referenceRegistryURL: {row}}
	refs := referenceRegistry(t, sources)
	for _, rows := range []any{
		[]map[string]any{{"url": row.URL, "extraction_status": "succeeded", "page_excerpts": []any{map[string]string{"id": refs.snapshot()[0].ID, "text": row.PageExcerpts[0]}}}},
		[]webEvidenceSource{{URL: row.URL, ExtractionStatus: "unavailable", PageExcerpts: row.PageExcerpts}},
	} {
		raw := referenceRegistryOutput(t, "searxng", rows)
		projected := refs.project([]toolCall{{Name: "search_web"}}, []json.RawMessage{raw}, sources)
		if blocks := referenceRegistryRows(t, projected[0])[0]["page_excerpts"]; string(blocks) != "[]" {
			t.Fatalf("ineligible or upstream-selected reference acquired authority: %s", blocks)
		}
	}
	sources[referenceRegistryURL][0].PagePublishedDate = "2026-09-04"
	raw := referenceRegistryOutput(t, "searxng", sources[referenceRegistryURL])
	projected := refs.project([]toolCall{{Name: "search_web"}}, []json.RawMessage{raw}, sources)
	if blocks := referenceRegistryRows(t, projected[0])[0]["page_excerpts"]; string(blocks) != "[]" {
		t.Fatal("mutated retained row bypassed synchronization and rebound a live reference")
	}
	for _, test := range []struct {
		call string
		raw  json.RawMessage
	}{
		{"search_web", referenceRegistryOutput(t, "tavily", []webEvidenceSource{row})},
		{"search_news", referenceRegistryOutput(t, "searxng", []webEvidenceSource{row})},
		{"search_web", json.RawMessage(`{"output":"invalid payload"}`)},
		{"search_web", json.RawMessage(`not JSON`)},
	} {
		got := refs.project([]toolCall{{Name: test.call}}, []json.RawMessage{test.raw}, sources)
		if !bytes.Equal(got[0], test.raw) {
			t.Errorf("out-of-scope or malformed output was changed: %s", test.raw)
		}
	}
}

func TestWebEvidenceReferencesReviewUsesStoredBoundRecordsAndWholeRecordBudget(t *testing.T) {
	const passage = "The telescope operates only on clear evenings."
	sources := map[string][]webEvidenceSource{referenceRegistryURL: {{URL: referenceRegistryURL, ExtractionStatus: "succeeded", PageExcerpts: []string{passage, "The separate garden closes at sunset."}}}}
	refs := referenceRegistry(t, sources)
	id := refs.snapshot()[0].ID
	claim := map[string]any{"support_excerpt_id": id, "text": "Candidate wording is not source evidence.", "support_quote": "Forged source replacement."}
	raw := string(referenceRegistryJSON(t, map[string]any{"claims": []any{claim, claim, map[string]string{"support_excerpt_id": "unknown"}}}))
	review := refs.review(raw)
	var records []webEvidenceReference
	if err := json.Unmarshal([]byte(review), &records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != id || len(records[0].Source.PageExcerpts) != 1 || records[0].Source.PageExcerpts[0] != passage || strings.Contains(review, "Forged") || strings.Contains(review, "Candidate wording") || strings.Contains(review, "separate garden") {
		t.Fatalf("review did not use one deduplicated stored bound record: %s", review)
	}
	sources[referenceRegistryURL][0].Title = strings.Repeat("long metadata ", 2000)
	refs.sync(sources)
	raw = string(referenceRegistryJSON(t, map[string]any{"claims": []any{map[string]string{"support_excerpt_id": refs.snapshot()[0].ID}}}))
	if got := refs.review(raw); got != "" {
		t.Fatalf("oversized review record was truncated or exceeded the byte budget: %d bytes", len(got))
	}
	if got := refs.review(`not JSON`); got != "" {
		t.Fatal("invalid candidate produced review references")
	}
}
