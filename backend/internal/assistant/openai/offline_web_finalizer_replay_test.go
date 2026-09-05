package openai

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// Re-run the last captured candidate through the deterministic finalizer only.
// No model, router, search, extractor, credentials or network client is used.
// Mechanical acceptance is not a semantic grade or a new end-to-end result.
func TestOfflineWebFinalizerReplay(t *testing.T) {
	path := os.Getenv("OFFLINE_WEB_FINALIZER_SOURCE")
	if path == "" {
		t.Skip("set OFFLINE_WEB_FINALIZER_SOURCE and OFFLINE_WEB_FINALIZER_REPORT for an offline diagnostic")
	}
	input, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(io.LimitReader(input, capturedEvidenceLimit+1))
	closeErr := input.Close()
	if readErr != nil || closeErr != nil || len(data) > capturedEvidenceLimit {
		t.Fatal("cannot read bounded captured report")
	}
	var captured struct {
		Mode           string `json:"mode"`
		TavilyFallback bool   `json:"tavily_fallback"`
		TavilyAttempts int    `json:"tavily_network_attempts"`
		ZeroTavily     bool   `json:"zero_tavily_attempts_verified"`
		Runs           []struct {
			Case     heldoutWebCase            `json:"case"`
			Response string                    `json:"response"`
			Searches []liveComparisonSearch    `json:"searches"`
			Reviews  []webEvidenceReviewRecord `json:"evidence_reviews"`
		} `json:"runs"`
	}
	if json.Unmarshal(data, &captured) != nil || captured.Mode != "full_stack_searxng_only_heldout_pilot" || captured.TavilyFallback || captured.TavilyAttempts != 0 || !captured.ZeroTavily {
		t.Fatal("requires a captured SearXNG-only full-stack pilot with zero Tavily attempts")
	}
	type replayRun struct {
		Case               heldoutWebCase `json:"case"`
		OriginalResponse   string         `json:"original_live_response"`
		CandidateJSON      string         `json:"unchanged_last_candidate_json"`
		OriginalRejections []string       `json:"original_last_rejections"`
		Rendered           string         `json:"offline_rendered"`
		Rejected           []string       `json:"offline_rejected"`
	}
	var runs []replayRun
	inferredRecorderOrder := false
	for _, run := range captured.Runs {
		if selected := os.Getenv("OFFLINE_WEB_FINALIZER_CASE"); selected != "" && selected != run.Case.ID {
			continue
		}
		if len(run.Reviews) == 0 {
			t.Fatalf("case %s has no captured finalizer review", run.Case.ID)
		}
		asOf, err := time.Parse(time.RFC3339, run.Case.AsOf)
		if err != nil {
			t.Fatal(err)
		}
		location, err := time.LoadLocation(run.Case.TimeZone)
		if err != nil {
			t.Fatal(err)
		}
		last := run.Reviews[len(run.Reviews)-1]
		searches := run.Searches
		if last.SchemaVersion == webEvidenceReferenceSchemaVersion {
			if last.CaptureOrderSHA256 == nil {
				inferredRecorderOrder = true
			} else {
				searches, err = orderRecordedWebCaptures(run.Searches, last.CaptureOrderSHA256)
				if err != nil {
					t.Fatalf("case %s: %v", run.Case.ID, err)
				}
			}
		}
		sources := make(map[string][]webEvidenceSource)
		for _, search := range searches {
			if search.Error != "" {
				continue
			}
			var payload struct {
				Provider string `json:"provider"`
			}
			if json.Unmarshal([]byte(search.Content), &payload) != nil || payload.Provider != "searxng" {
				t.Fatal("invalid or non-SearXNG captured search")
			}
			output, err := json.Marshal(toolOutput{Type: "function_call_output", Output: search.Content})
			if err != nil {
				t.Fatal(err)
			}
			// Preserve bounded collection; future v2 captures supply exact
			// ingestion order rather than concurrent recorder arrival order.
			collectWebEvidence(sources, []toolCall{{Name: "search_web"}}, []json.RawMessage{output})
		}
		var rendered string
		var rejected []string
		switch last.SchemaVersion {
		case "":
			rendered, rejected = renderWebEvidence(last.CandidateJSON, sources, run.Case.Question, asOf.In(location))
		case webEvidenceReferenceSchemaVersion:
			refs, err := loadRecordedWebReferences(last.ExcerptReferences, sources)
			if err != nil {
				t.Fatalf("case %s: %v", run.Case.ID, err)
			}
			rendered, rejected = renderReferencedWebEvidence(last.CandidateJSON, refs, run.Case.Question, asOf.In(location))
		default:
			t.Fatalf("unsupported captured evidence schema %q", last.SchemaVersion)
		}
		runs = append(runs, replayRun{run.Case, run.Response, last.CandidateJSON, last.Rejected, rendered, rejected})
	}
	if len(runs) == 0 {
		t.Fatal("no captured cases selected")
	}
	digest := sha256.Sum256(data)
	report := struct {
		Mode   string      `json:"mode"`
		Source string      `json:"source_report"`
		SHA256 string      `json:"source_report_sha256"`
		Scope  string      `json:"scope"`
		Runs   []replayRun `json:"runs"`
	}{"offline_deterministic_finalizer_replay", path, hex.EncodeToString(digest[:]),
		"No live/model/network calls. Unchanged last candidate, question, clock and captured searches, with current deterministic validation/rendering only. This is not a new model answer, fresh retrieval, quality regrade, or semantic proof. Original live results remain unchanged; review any newly accepted wording independently.", runs}
	if inferredRecorderOrder {
		report.Scope += " At least one v2 capture lacks ingestion-order hashes: collection order is inferred from recorder arrival order, which can differ for parallel calls. Retained-row eviction order is not independently proven for those captures."
	}
	output, err := os.OpenFile(os.Getenv("OFFLINE_WEB_FINALIZER_REPORT"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	writeErr := encoder.Encode(report)
	closeErr = output.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal("cannot write new offline report")
	}
	t.Logf("Replayed %d captured final candidates offline; no quality grade implied", len(runs))
}

// Reconstruct only evaluation-time ID bindings, after checking them against
// the independently collected raw tool corpus. Snapshot data is never an
// alternate source corpus and cannot grant a new URL, date, or passage.
func loadRecordedWebReferences(snapshot []webEvidenceReference, sources map[string][]webEvidenceSource) (*webEvidenceReferences, error) {
	if len(snapshot) > 40*4*maxReferencedBlocksPerSource {
		return nil, errors.New("recorded reference count exceeds the collection budget")
	}
	refs := &webEvidenceReferences{active: make(map[string]webEvidenceSource)}
	for _, reference := range snapshot {
		if reference.ID == "" || len(reference.ID) > 100 || len(reference.Source.PageExcerpts) != 1 || reference.Source.ExtractionStatus != "succeeded" {
			return nil, errors.New("recorded reference lacks a bounded fetched identity")
		}
		if utf8.RuneCountInString(reference.Source.PageExcerpts[0]) > 1000 {
			return nil, errors.New("recorded reference exceeds the whole-block bound")
		}
		if _, duplicate := refs.active[reference.ID]; duplicate {
			return nil, errors.New("duplicate recorded reference identity")
		}
		matched := false
		for _, row := range sources[reference.Source.URL] {
			for index, block := range row.PageExcerpts {
				if index >= maxReferencedBlocksPerSource {
					break
				}
				bound := cloneReferenceSource(row)
				bound.PageExcerpts = []string{block}
				if sameReferenceSource(bound, reference.Source) && sourceContainsQuote([]webEvidenceSource{bound}, block) {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if !matched {
			return nil, errors.New("recorded reference is not bound to a retained raw capture")
		}
		refs.active[reference.ID] = cloneReferenceSource(reference.Source)
	}
	return refs, nil
}

func TestRecordedWebReferencesRoundTripRequiresActualCapturedRow(t *testing.T) {
	const passage = "Section: Observatory\nThe public telescope has a 40-centimeter aperture."
	sources := map[string][]webEvidenceSource{evidenceFixtureURL: {{URL: evidenceFixtureURL, ExtractionStatus: "succeeded", PageExcerpts: []string{passage}, PagePublishedDate: "2026-09-04"}}}
	refs, err := newWebEvidenceReferences()
	if err != nil {
		t.Fatal(err)
	}
	refs.sync(sources)
	encoded, _ := json.Marshal(refs.snapshot())
	var recorded []webEvidenceReference
	if err := json.Unmarshal(encoded, &recorded); err != nil {
		t.Fatal(err)
	}
	restored, err := loadRecordedWebReferences(recorded, sources)
	if err != nil {
		t.Fatal(err)
	}
	claim := webReferencedEvidenceClaim{Text: "The telescope has a 40-centimeter aperture.", SupportExcerptID: recorded[0].ID}
	raw, _ := json.Marshal(webReferencedEvidenceAnswer{Claims: []webReferencedEvidenceClaim{claim}})
	before, beforeRejected := renderReferencedWebEvidence(string(raw), refs, "Check the telescope aperture.", evidenceFixtureNow())
	after, afterRejected := renderReferencedWebEvidence(string(raw), restored, "Check the telescope aperture.", evidenceFixtureNow())
	if before != after || len(beforeRejected) != 0 || len(afterRejected) != 0 {
		t.Fatalf("reference replay changed supported answer: before=%q after=%q rejected=%v/%v", before, after, beforeRejected, afterRejected)
	}
	for _, mutate := range []func(*webEvidenceReference){
		func(r *webEvidenceReference) { r.Source.PagePublishedDate = "2026-09-05" },
		func(r *webEvidenceReference) { r.Source.URL = "https://another.example/info" },
		func(r *webEvidenceReference) { r.Source.PageExcerpts = []string{passage + " Admission is free."} },
		func(r *webEvidenceReference) { r.Source.ExtractionStatus = "unavailable" },
	} {
		forged := recorded[0]
		forged.Source = cloneReferenceSource(forged.Source)
		mutate(&forged)
		if _, err := loadRecordedWebReferences([]webEvidenceReference{forged}, sources); err == nil {
			t.Fatal("snapshot forged source metadata or text without a matching raw capture")
		}
	}
	if _, err := loadRecordedWebReferences(append(recorded, recorded[0]), sources); err == nil {
		t.Fatal("duplicate reference IDs were accepted")
	}
	// Normalization must not hide a raw block the live registry cannot mint.
	oversized := recorded[0]
	oversized.Source = cloneReferenceSource(oversized.Source)
	oversized.Source.PageExcerpts = []string{passage + strings.Repeat(" ", 1000)}
	oversizedCorpus := map[string][]webEvidenceSource{evidenceFixtureURL: {oversized.Source}}
	if _, err := loadRecordedWebReferences([]webEvidenceReference{oversized}, oversizedCorpus); err == nil {
		t.Fatal("raw oversized reference passed merely because its normalized text was short")
	}
}
