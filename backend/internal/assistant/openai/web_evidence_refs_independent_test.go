package openai

import (
	"encoding/json"
	"strings"
	"testing"
)

func independentEvidenceReferences(t *testing.T, sources map[string][]webEvidenceSource) *webEvidenceReferences {
	t.Helper()
	refs, err := newWebEvidenceReferences()
	if err != nil {
		t.Fatal(err)
	}
	refs.sync(sources)
	return refs
}

func independentEvidenceReferenceID(t *testing.T, refs *webEvidenceReferences, sourceURL, excerpt string) string {
	t.Helper()
	for _, reference := range refs.snapshot() {
		if reference.Source.URL == sourceURL && len(reference.Source.PageExcerpts) == 1 && reference.Source.PageExcerpts[0] == excerpt {
			return reference.ID
		}
	}
	t.Fatalf("expected reference for exact source/block %q %q", sourceURL, excerpt)
	return ""
}

func independentReferencedClaim(id, text string) map[string]any {
	return map[string]any{"support_excerpt_id": id, "text": text, "event_date": "", "date_quote": "", "schedule_quote": "", "starts_at": "", "ends_at": ""}
}

func independentReferencedAnswer(t *testing.T, claims ...map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"claims": claims, "limitations": []string{}})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestIndependentEvidenceRefsUnknownAndCrossTurnIDsCannotReplaceSupportedControl(t *testing.T) {
	const passage = "Aurora Observatory offers guided telescope viewing."
	sources := map[string][]webEvidenceSource{evidenceFixtureURL: {{URL: evidenceFixtureURL, ExtractionStatus: "succeeded", PageExcerpts: []string{passage}}}}
	refs := independentEvidenceReferences(t, sources)
	id := independentEvidenceReferenceID(t, refs, evidenceFixtureURL, passage)
	otherTurn := independentEvidenceReferences(t, sources)
	otherID := independentEvidenceReferenceID(t, otherTurn, evidenceFixtureURL, passage)
	if id == otherID {
		t.Fatal("separate turns reused an evidence reference namespace")
	}
	for _, unknown := range []string{"", "untrusted-page-invented-id", otherID} {
		if _, ok := refs.resolve(unknown); ok {
			t.Errorf("unknown or other-turn id resolved: %q", unknown)
		}
		raw := independentReferencedAnswer(t, independentReferencedClaim(unknown, "Fabricated reference should never appear."), independentReferencedClaim(id, passage))
		response, rejected := renderReferencedWebEvidence(raw, refs, "Tell me about public telescope viewing.", evidenceFixtureNow())
		if !strings.Contains(response, passage) || strings.Contains(response, "Fabricated reference") || len(rejected) == 0 {
			t.Errorf("invalid reference acquired authority or discarded the positive control: response=%q rejected=%v", response, rejected)
		}
	}
}

func TestIndependentEvidenceRefsResolveOneImmutableBlockAndOriginalRowMetadata(t *testing.T) {
	const first = "The garden entrance costs $11 per adult."
	const second = "The separate observatory entrance costs $29 per adult."
	sources := map[string][]webEvidenceSource{evidenceFixtureURL: {{URL: evidenceFixtureURL, Title: "Aurora source", ExtractionStatus: "succeeded", PageExcerpts: []string{first, second}, PagePublishedDate: "2026-09-03T16:00:00Z", PublishedDate: "1999-01-01"}}}
	refs := independentEvidenceReferences(t, sources)
	id := independentEvidenceReferenceID(t, refs, evidenceFixtureURL, first)
	secondID := independentEvidenceReferenceID(t, refs, evidenceFixtureURL, second)
	if id == secondID {
		t.Fatal("independent source blocks share a reference")
	}
	resolved, ok := refs.resolve(id)
	if !ok || len(resolved.PageExcerpts) != 1 || resolved.PageExcerpts[0] != first || resolved.PagePublishedDate != "2026-09-03T16:00:00Z" || resolved.URL != evidenceFixtureURL {
		t.Fatalf("reference lost its exact source/block/row metadata: %#v", resolved)
	}
	resolved.PageExcerpts[0] = "Caller modified this passage."
	for _, snapshot := range refs.snapshot() {
		if snapshot.ID == id {
			snapshot.Source.PageExcerpts[0] = "Caller modified the snapshot."
		}
	}
	resolved, ok = refs.resolve(id)
	if !ok || resolved.PageExcerpts[0] != first {
		t.Fatal("returned source data mutated the authoritative reference registry")
	}
	refs.sync(sources)
	if independentEvidenceReferenceID(t, refs, evidenceFixtureURL, first) != id {
		t.Fatal("unchanged retained passage lost its stable within-turn reference")
	}
	delete(sources, evidenceFixtureURL)
	refs.sync(sources)
	if _, ok := refs.resolve(id); ok {
		t.Fatal("reference still resolved after its complete source row was evicted")
	}
}

func TestIndependentEvidenceRefsFailedQuickAndEmptyFetchesNeverPromoteSnippets(t *testing.T) {
	const control = "Aurora Observatory offers public guided viewing."
	const lead = "The unsupported entrance fee is $37 per visitor."
	for _, status := range []string{"unavailable", "not_requested", "succeeded"} {
		t.Run(status, func(t *testing.T) {
			const leadURL = "https://aurora.example/unverified"
			sources := map[string][]webEvidenceSource{
				evidenceFixtureURL: {{URL: evidenceFixtureURL, PageExcerpts: []string{control}, ExtractionStatus: "succeeded"}},
				leadURL:            {{URL: leadURL, Title: lead, Snippet: lead, DiscoverySnippet: lead, ExtractionStatus: status, PageExcerpts: []string{"", "  "}}},
			}
			refs := independentEvidenceReferences(t, sources)
			if got := refs.snapshot(); len(got) != 1 || got[0].Source.URL != evidenceFixtureURL {
				t.Fatalf("discovery-only %q generated factual passage references: %#v", status, got)
			}
			id := independentEvidenceReferenceID(t, refs, evidenceFixtureURL, control)
			response, rejected := renderReferencedWebEvidence(independentReferencedAnswer(t, independentReferencedClaim(id, control)), refs, "Verify public viewing.", evidenceFixtureNow())
			if !strings.Contains(response, control) || len(rejected) != 0 || strings.Contains(response, "$37") {
				t.Errorf("discovery-only rows contaminated or disabled a fetched positive control: %q %v", response, rejected)
			}
		})
	}
}

func TestIndependentEvidenceRefsContradictoryFetchStatusCannotMintPassageIDs(t *testing.T) {
	const control = "The public garden has a riverside footpath."
	const unsupported = "A contradictory quick result says entry costs $37."
	for _, status := range []string{"unavailable", "not_requested", ""} {
		refs := independentEvidenceReferences(t, map[string][]webEvidenceSource{
			evidenceFixtureURL:           {{URL: evidenceFixtureURL, PageExcerpts: []string{control}, ExtractionStatus: "succeeded"}},
			"https://other.example/lead": {{URL: "https://other.example/lead", PageExcerpts: []string{unsupported}, ExtractionStatus: status}},
		})
		if snapshot := refs.snapshot(); len(snapshot) != 1 || snapshot[0].Source.URL != evidenceFixtureURL {
			t.Errorf("non-successful status %q minted a modern reference from contradictory upstream blocks: %#v", status, snapshot)
		}
		independentEvidenceReferenceID(t, refs, evidenceFixtureURL, control)
	}
}

func TestIndependentEvidenceRefsNumbersCannotPoolAcrossBlocksOrInventCurrency(t *testing.T) {
	const menu = "Menu item: Garden Bowl; item-price=11.00; Rice and beans."
	const otherPrice = "The observatory ticket price is $29 per adult."
	refs := independentEvidenceReferences(t, map[string][]webEvidenceSource{evidenceFixtureURL: {{URL: evidenceFixtureURL, PageExcerpts: []string{menu, otherPrice}, ExtractionStatus: "succeeded"}}})
	id := independentEvidenceReferenceID(t, refs, evidenceFixtureURL, menu)
	for _, text := range []string{"Garden Bowl has a listed item-price of $11.00.", "Garden Bowl costs $29."} {
		response, rejected := renderReferencedWebEvidence(independentReferencedAnswer(t, independentReferencedClaim(id, text)), refs, "What does the Garden Bowl menu list?", evidenceFixtureNow())
		if len(rejected) == 0 || strings.Contains(response, "$11.00") || strings.Contains(response, "$29") {
			t.Errorf("reference pooled another block's price or invented dollar currency: response=%q rejected=%v", response, rejected)
		}
	}
	const supported = "Garden Bowl has a listed item-price of 11.00."
	response, rejected := renderReferencedWebEvidence(independentReferencedAnswer(t, independentReferencedClaim(id, supported)), refs, "What does the Garden Bowl menu list?", evidenceFixtureNow())
	if !strings.Contains(response, supported) || len(rejected) != 0 {
		t.Errorf("literal unitless item price positive control failed: %q %v", response, rejected)
	}
}

func TestIndependentEvidenceRefsRelativeEventCannotBorrowAnotherCaptureDate(t *testing.T) {
	const announcement = "Aurora Observatory opened its telescope, the club announced Thursday."
	const unrelated = "The Aurora garden opened its new footpath, the club announced Thursday."
	const published = "2026-09-03T16:00:00Z"
	for _, withOwnDate := range []bool{false, true} {
		row := webEvidenceSource{URL: evidenceFixtureURL, PageExcerpts: []string{announcement}, ExtractionStatus: "succeeded", PublishedDate: published}
		if withOwnDate {
			row.PagePublishedDate = published
		}
		refs := independentEvidenceReferences(t, map[string][]webEvidenceSource{evidenceFixtureURL: {row, {URL: evidenceFixtureURL, PageExcerpts: []string{unrelated}, ExtractionStatus: "succeeded", PagePublishedDate: published}}})
		id := independentEvidenceReferenceID(t, refs, evidenceFixtureURL, announcement)
		claim := independentReferencedClaim(id, "Aurora Observatory opened its telescope.")
		claim["event_date"], claim["date_quote"] = "2026-09-03", announcement
		response, rejected := renderReferencedWebEvidence(independentReferencedAnswer(t, claim), refs, "What changed today?", evidenceFixtureNow())
		if withOwnDate {
			if !strings.Contains(response, "Aurora Observatory opened its telescope.") || len(rejected) != 0 {
				t.Errorf("same-row fetched event publication positive control failed: %q %v", response, rejected)
			}
		} else if len(rejected) == 0 || response != webEvidenceAbstention("What changed today?") {
			t.Errorf("event borrowed a discovery or another capture's date: %q %v", response, rejected)
		}
	}
}

func TestIndependentEvidenceRefsScheduleCannotBorrowAnotherSourceBlock(t *testing.T) {
	const activity = "The Aurora telescope offers guided public viewing."
	const schedule = "Aurora telescope guided viewing runs daily from 8pm to 11pm."
	for _, joined := range []bool{false, true} {
		blocks := []string{activity, schedule}
		if joined {
			blocks = []string{activity + " " + schedule}
		}
		refs := independentEvidenceReferences(t, map[string][]webEvidenceSource{evidenceFixtureURL: {{URL: evidenceFixtureURL, PageExcerpts: blocks, ExtractionStatus: "succeeded"}}})
		id := independentEvidenceReferenceID(t, refs, evidenceFixtureURL, blocks[0])
		claim := independentReferencedClaim(id, activity)
		claim["schedule_quote"], claim["starts_at"], claim["ends_at"] = schedule, "2026-09-03T20:00:00-07:00", "2026-09-03T23:00:00-07:00"
		response, rejected := renderReferencedWebEvidence(independentReferencedAnswer(t, claim), refs, "What can I do tonight?", evidenceFixtureNow())
		if joined {
			if !strings.Contains(response, activity) || len(rejected) != 0 {
				t.Errorf("same-block activity/schedule positive control failed: %q %v", response, rejected)
			}
		} else if len(rejected) == 0 || response != webEvidenceAbstention("What can I do tonight?") {
			t.Errorf("independent schedule block supplied the selected reference's activity frame: %q %v", response, rejected)
		}
	}
}

func TestIndependentEvidenceRefsVenueHoursCannotBorrowAnotherBlocksEventFrame(t *testing.T) {
	const schedule = "Aurora guided viewing runs daily from 8pm to 11pm."
	const venue = "Page structured data: Place Aurora Observatory; openingHours=Thursday 20:00-23:00; description=" + schedule
	const event = "Page structured data: Event Aurora guided viewing; startDate=2026-09-03T20:00:00-07:00; endDate=2026-09-03T23:00:00-07:00; description=" + schedule
	refs := independentEvidenceReferences(t, map[string][]webEvidenceSource{evidenceFixtureURL: {{URL: evidenceFixtureURL, PageExcerpts: []string{venue, event}, ExtractionStatus: "succeeded"}}})
	for _, block := range []string{venue, event} {
		claim := independentReferencedClaim(independentEvidenceReferenceID(t, refs, evidenceFixtureURL, block), "Aurora offers guided viewing.")
		claim["schedule_quote"], claim["starts_at"], claim["ends_at"] = schedule, "2026-09-03T20:00:00-07:00", "2026-09-03T23:00:00-07:00"
		response, rejected := renderReferencedWebEvidence(independentReferencedAnswer(t, claim), refs, "What can I do tonight?", evidenceFixtureNow())
		if block == venue {
			if len(rejected) == 0 || response != webEvidenceAbstention("What can I do tonight?") {
				t.Errorf("venue-hours reference borrowed an independent block's event identity: %q %v", response, rejected)
			}
		} else if len(rejected) != 0 || !strings.Contains(response, "Aurora offers guided viewing.") {
			t.Errorf("same-reference explicit event positive control failed: %q %v", response, rejected)
		}
	}
}

func TestIndependentEvidenceRefsRejectMixedLegacyFields(t *testing.T) {
	const passage = "Aurora Observatory offers guided telescope viewing."
	refs := independentEvidenceReferences(t, map[string][]webEvidenceSource{evidenceFixtureURL: {{URL: evidenceFixtureURL, ExtractionStatus: "succeeded", PageExcerpts: []string{passage}}}})
	id := independentEvidenceReferenceID(t, refs, evidenceFixtureURL, passage)
	for _, field := range []string{"source_url", "support_quote"} {
		claim := independentReferencedClaim(id, passage)
		claim[field] = ""
		response, rejected := renderReferencedWebEvidence(independentReferencedAnswer(t, claim), refs, "Describe the observatory.", evidenceFixtureNow())
		if len(rejected) == 0 || response != webEvidenceAbstention("Describe the observatory.") {
			t.Errorf("mixed legacy field %q was accepted: %q %v", field, response, rejected)
		}
	}
}

func TestIndependentEvidenceRefsCapturedNASAUsesTwoPassagesWithoutAcceptingOldEllipsis(t *testing.T) {
	raw := readWebResearchTestCorpus(t, "heldout-pilot-live9-free-engine-2026-09-04.json")
	var capture struct {
		Runs []struct {
			Case     struct{ ID, Question string } `json:"case"`
			Searches []struct{ Content string }    `json:"searches"`
			Reviews  []webEvidenceReviewRecord     `json:"evidence_reviews"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(raw, &capture); err != nil {
		t.Fatal(err)
	}
	for _, run := range capture.Runs {
		if run.Case.ID != "nasa_full_moon_eclipse_geometry" {
			continue
		}
		sources := make(map[string][]webEvidenceSource)
		for _, search := range run.Searches {
			var payload struct {
				Results []webEvidenceSource `json:"results"`
			}
			if err := json.Unmarshal([]byte(search.Content), &payload); err != nil {
				t.Fatal(err)
			}
			for _, row := range payload.Results {
				sources[row.URL] = append(sources[row.URL], row)
			}
		}
		const sourceURL = "https://science.nasa.gov/moon/eclipses/"
		var tilt, shadow string
		for _, row := range sources[sourceURL] {
			for _, block := range row.PageExcerpts {
				if strings.Contains(block, "This tilt is the reason why we have occasional eclipses instead of eclipses every month.") {
					tilt = block
				}
				if strings.Contains(block, "Lunar eclipses occur at the full Moon phase") && strings.Contains(block, "shadow falls upon the surface of the Moon") {
					shadow = block
				}
			}
		}
		if tilt == "" || shadow == "" || len(run.Reviews) == 0 {
			t.Fatal("saved NASA geometry passages or original failed candidate are missing")
		}
		refs := independentEvidenceReferences(t, sources)
		const tiltClaim = "The Moon's tilted orbit prevents an eclipse at every full Moon."
		const shadowClaim = "A lunar eclipse occurs at full Moon when Earth lies between the Sun and Moon and Earth's shadow falls on the Moon."
		answer := independentReferencedAnswer(t,
			independentReferencedClaim(independentEvidenceReferenceID(t, refs, sourceURL, tilt), tiltClaim),
			independentReferencedClaim(independentEvidenceReferenceID(t, refs, sourceURL, shadow), shadowClaim),
		)
		response, rejected := renderReferencedWebEvidence(answer, refs, run.Case.Question, evidenceFixtureNow())
		if !strings.Contains(response, tiltClaim) || !strings.Contains(response, shadowClaim) || len(rejected) != 0 {
			t.Errorf("two separately referenced real NASA passages lost complete geometry coverage: %q %v", response, rejected)
		}
		// This positive fixture establishes passage binding and retained coverage,
		// not a general machine guarantee of semantic or scientific entailment.
		old := run.Reviews[0].CandidateJSON
		if !strings.Contains(old, "...") {
			t.Fatal("saved NASA failure no longer contains its actual invented ellipsis")
		}
		oldResponse, oldRejected := renderWebEvidence(old, sources, run.Case.Question, evidenceFixtureNow())
		if oldResponse != webEvidenceAbstention(run.Case.Question) || len(oldRejected) == 0 {
			t.Errorf("passage references weakened the old contiguous-quotation guard: %q %v", oldResponse, oldRejected)
		}
		return
	}
	t.Fatal("saved NASA case is absent")
}
