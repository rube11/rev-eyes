package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

// Match a multiset, not a set: two identical successful payloads are two
// collector inputs. Equal-byte duplicates have identical source metadata, so
// consuming their recorder slots in either order cannot change bounded rows.
func orderRecordedWebCaptures(searches []liveComparisonSearch, order []string) ([]liveComparisonSearch, error) {
	byDigest := make(map[string][]liveComparisonSearch)
	count := 0
	for _, search := range searches {
		if search.Error != "" {
			continue
		}
		digest, ok := webEvidenceCaptureDigest(search.Content)
		if !ok {
			return nil, errors.New("invalid or non-SearXNG captured search in ingestion order")
		}
		byDigest[digest] = append(byDigest[digest], search)
		count++
	}
	if len(order) != count {
		return nil, errors.New("capture-order hashes do not match the successful raw capture count")
	}
	ordered := make([]liveComparisonSearch, 0, count)
	for _, digest := range order {
		matches := byDigest[digest]
		if len(matches) == 0 {
			return nil, errors.New("capture-order hash is unknown or exceeds its raw capture multiplicity")
		}
		ordered = append(ordered, matches[0])
		byDigest[digest] = matches[1:]
	}
	return ordered, nil
}

type captureOrderFixtureTool struct {
	run func(context.Context, tool.Scope, json.RawMessage) (tool.Result, error)
}

func (f captureOrderFixtureTool) Spec() tool.Spec {
	return tool.Spec{Name: "search_web", Description: "Offline capture-order fixture", Parameters: json.RawMessage(testSchema), ReadOnly: true}
}

func (f captureOrderFixtureTool) Execute(ctx context.Context, scope tool.Scope, args json.RawMessage) (tool.Result, error) {
	return f.run(ctx, scope, args)
}

func captureOrderFixtureValue(args json.RawMessage) string {
	var input struct{ Value string }
	_ = json.Unmarshal(args, &input)
	return input.Value
}

func TestWebEvidenceCaptureOrderReplaysForcedParallelArrivalAtRowCap(t *testing.T) {
	const endpoint = "https://aurora.example/collection"
	payloads := make(map[string]string)
	for _, name := range []string{"A", "B", "C", "D", "E"} {
		payloads[name] = referenceWorkflowJSON(t, map[string]any{"provider": "searxng", "results": []webEvidenceSource{{URL: endpoint, Title: "Capture " + name, ExtractionStatus: "succeeded", PageExcerpts: []string{"Capture " + name + " describes the public garden."}, PagePublishedDate: "2026-09-03"}}})
	}
	secondReserved := make(chan struct{})
	recorder := &comparisonSearchTool{delegate: captureOrderFixtureTool{run: func(_ context.Context, _ tool.Scope, args json.RawMessage) (tool.Result, error) {
		name := captureOrderFixtureValue(args)
		if name == "B" {
			close(secondReserved) // comparisonSearchTool has already reserved B.
		}
		return tool.Result{Content: payloads[name]}, nil
	}}}
	// The first model call cannot enter the recorder before the second call
	// reserves its slot. This deterministically reproduces goroutine reordering.
	fixture := captureOrderFixtureTool{run: func(ctx context.Context, scope tool.Scope, args json.RawMessage) (tool.Result, error) {
		if captureOrderFixtureValue(args) == "A" {
			select {
			case <-secondReserved:
			case <-ctx.Done():
				return tool.Result{}, ctx.Err()
			}
		}
		return recorder.Execute(ctx, scope, args)
	}}
	requests := 0
	var selectedID string
	agent := testAgent(t, fixture, func(w http.ResponseWriter, r *http.Request) {
		var request createRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		index := requests
		requests++
		if index < 4 {
			names := []string{"A", "B"}
			if index > 0 {
				names = []string{[]string{"C", "D", "E"}[index-1]}
			}
			var calls []any
			for _, name := range names {
				calls = append(calls, map[string]any{"type": "function_call", "call_id": "capture-" + name, "name": "search_web", "arguments": fmt.Sprintf(`{"value":%q}`, name)})
			}
			writeJSON(t, w, map[string]any{"output": calls})
			return
		}
		if index > 5 {
			t.Fatal("capture-order fixture exceeded four tool rounds plus candidate/review")
		}
		if len(request.Tools) != 0 {
			t.Error("candidate/review at the round cap reopened tools")
		}
		for _, raw := range request.Input {
			var output toolOutput
			var payload struct {
				Results []struct {
					Title  string
					Blocks []struct{ ID, Text string } `json:"page_excerpts"`
				} `json:"results"`
			}
			if json.Unmarshal(raw, &output) == nil && output.Type == "function_call_output" && json.Unmarshal([]byte(output.Output), &payload) == nil {
				for _, row := range payload.Results {
					if row.Title == "Capture B" && len(row.Blocks) == 1 {
						selectedID = row.Blocks[0].ID
					}
				}
			}
		}
		if selectedID == "" {
			t.Fatal("positive retained B reference was absent from real projected output")
		}
		answer := referenceWorkflowAnswer(t, selectedID, "Capture B describes the public garden.")
		writeJSON(t, w, map[string]any{"output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": answer}}}}})
	})
	agent.model, agent.now = "gpt-5.4-mini", evidenceFixtureNow
	var reviews []webEvidenceReviewRecord
	agent.onWebEvidenceReview = func(record webEvidenceReviewRecord) { reviews = append(reviews, record) }
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	response, err := agent.Respond(ctx, tool.Scope{}, "Describe the public garden.", session.Conversation{}, nil)
	if err != nil || requests != 6 || len(reviews) != 2 || !strings.Contains(response, "Capture B describes the public garden.") {
		t.Fatalf("bounded parallel workflow failed: response=%q err=%v requests=%d reviews=%d", response, err, requests, len(reviews))
	}
	searches := recorder.snapshot()
	if len(searches) != 5 || searches[0].Content != payloads["B"] || searches[1].Content != payloads["A"] {
		t.Fatal("fixture did not force a genuine recorder/collector order difference")
	}
	var expected []string
	for _, name := range []string{"A", "B", "C", "D", "E"} {
		digest, _ := webEvidenceCaptureDigest(payloads[name])
		expected = append(expected, digest)
	}
	for _, review := range reviews {
		if !reflect.DeepEqual(review.CaptureOrderSHA256, expected) {
			t.Fatalf("review captured arrival or projected bytes rather than raw ingestion order: got=%v want=%v", review.CaptureOrderSHA256, expected)
		}
	}
	// Serialize the actual captured review, including its metadata and order.
	var final webEvidenceReviewRecord
	if err := json.Unmarshal([]byte(referenceWorkflowJSON(t, reviews[1])), &final); err != nil {
		t.Fatal(err)
	}
	collect := func(ordered []liveComparisonSearch) map[string][]webEvidenceSource {
		sources := make(map[string][]webEvidenceSource)
		for _, search := range ordered {
			output, _ := json.Marshal(toolOutput{Output: search.Content})
			collectWebEvidence(sources, []toolCall{{Name: "search_web"}}, []json.RawMessage{output})
		}
		return sources
	}
	if _, err := loadRecordedWebReferences(final.ExcerptReferences, collect(searches)); err == nil {
		t.Fatal("recorder-order negative control unexpectedly retained the same four source rows")
	}
	ordered, err := orderRecordedWebCaptures(searches, final.CaptureOrderSHA256)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := loadRecordedWebReferences(final.ExcerptReferences, collect(ordered))
	if err != nil {
		t.Fatal(err)
	}
	replayed, rejected := renderReferencedWebEvidence(final.CandidateJSON, restored, "Describe the public garden.", evidenceFixtureNow())
	if replayed != response || !reflect.DeepEqual(rejected, final.Rejected) {
		t.Fatalf("ordered replay changed the actual final answer or metadata-bound validation: live=%q replay=%q rejected=%v/%v", response, replayed, final.Rejected, rejected)
	}
}

func TestWebEvidenceCaptureOrderRequiresExactDuplicateAwareRawMultiset(t *testing.T) {
	contentA := `{"provider":"searxng","results":[]}`
	contentB := `{"provider":"searxng", "results":[]}` // Raw whitespace must not be canonicalized away.
	hashA, _ := webEvidenceCaptureDigest(contentA)
	hashB, _ := webEvidenceCaptureDigest(contentB)
	searches := []liveComparisonSearch{{Content: contentA}, {Content: contentB}, {Content: contentA}, {Error: "failed search", Content: contentA}}
	for _, order := range [][]string{{hashA, hashA, hashB}, {hashB, hashA, hashA}} {
		got, err := orderRecordedWebCaptures(searches, order)
		if err != nil || len(got) != 3 {
			t.Fatalf("legitimate duplicate-aware order failed: %#v %v", got, err)
		}
		for index, search := range got {
			digest, _ := webEvidenceCaptureDigest(search.Content)
			if digest != order[index] {
				t.Error("raw-byte payload order was not reproduced")
			}
		}
	}
	for _, order := range [][]string{
		nil, {}, {hashA, hashB}, {hashA, hashB, hashA, hashA},
		{hashA, hashB, hashB}, {hashA, hashB, strings.Repeat("f", 64)},
	} {
		if _, err := orderRecordedWebCaptures(searches, order); err == nil {
			t.Errorf("missing, extra, tampered or wrong-multiplicity hash list accepted: %v", order)
		}
	}
	for _, altered := range [][]liveComparisonSearch{
		searches[:2], append(append([]liveComparisonSearch(nil), searches...), liveComparisonSearch{Content: contentB}),
		{{Content: `{"provider":"tavily","results":[]}`}},
	} {
		if _, err := orderRecordedWebCaptures(altered, []string{hashA, hashB, hashA}); err == nil {
			t.Fatal("missing, extra or non-SearXNG raw corpus was accepted")
		}
	}
}
