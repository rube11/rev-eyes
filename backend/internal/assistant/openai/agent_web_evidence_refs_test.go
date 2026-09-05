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

	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const (
	referenceOpticsURL = "https://aurora.example/optics"
	referenceFeesURL   = "https://aurora.example/fees"
	referenceOptics    = "Mirrors focus incoming light onto a detector."
	referenceFees      = "Exhibit admission is $8 per visitor; telescope viewing is free."
)

type referenceWorkflowRun struct {
	search   *recordingTool
	requests []createRequest
	reviews  []webEvidenceReviewRecord
}

// Like testAgent, this uses an in-memory HTTP recorder, never a model or search
// service. The script must discover reference IDs in the actual tool output.
func referenceWorkflowAgent(t *testing.T, model, payload string, maxRequests int, script func(int, createRequest) evidenceAgentStep) (*Agent, *referenceWorkflowRun) {
	t.Helper()
	run := &referenceWorkflowRun{search: &recordingTool{name: "search_web", result: tool.Result{Content: payload}}}
	agent := testAgent(t, run.search, func(w http.ResponseWriter, r *http.Request) {
		var request createRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		run.requests = append(run.requests, request)
		index := len(run.requests) - 1
		if index >= maxRequests {
			t.Errorf("unexpected model request %d exceeds script bound %d", index+1, maxRequests)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		step := script(index, request)
		if step.toolName != "" {
			writeJSON(t, w, map[string]any{"output": []any{map[string]any{
				"type": "function_call", "call_id": fmt.Sprintf("reference-call-%d", index), "name": step.toolName, "arguments": `{"value":"Aurora optics and fees"}`,
			}}})
			return
		}
		writeJSON(t, w, map[string]any{"output": []any{map[string]any{
			"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": step.text}},
		}}})
	})
	agent.model, agent.now = model, evidenceFixtureNow
	agent.onWebEvidenceReview = func(record webEvidenceReviewRecord) { run.reviews = append(run.reviews, record) }
	return agent, run
}

func referenceWorkflowJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func referenceWorkflowPayload(t *testing.T, provider string) string {
	t.Helper()
	return referenceWorkflowJSON(t, map[string]any{"provider": provider, "results": []webEvidenceSource{
		{URL: referenceOpticsURL, Title: "Aurora optics", Snippet: "Discovery-only optics lead.", DiscoverySnippet: "Discovery-only optics lead.", PageExcerpts: []string{"Section: Aurora optics\n" + referenceOptics}, ExtractionStatus: "succeeded", PagePublishedDate: "2026-09-03"},
		{URL: referenceFeesURL, Title: "Aurora visitor fees", Snippet: "Discovery-only fees lead.", DiscoverySnippet: "Discovery-only fees lead.", PageExcerpts: []string{"Section: Aurora visitor fees\n" + referenceFees}, ExtractionStatus: "succeeded"},
	}})
}

func referenceWorkflowToolOutput(t *testing.T, request createRequest) string {
	t.Helper()
	var outputs []string
	for _, raw := range request.Input {
		var output toolOutput
		if json.Unmarshal(raw, &output) == nil && output.Type == "function_call_output" {
			outputs = append(outputs, output.Output)
		}
	}
	if len(outputs) != 1 {
		t.Fatalf("want exactly one retained search output, got %d", len(outputs))
	}
	return outputs[0]
}

func referenceWorkflowIDs(t *testing.T, request createRequest) map[string]string {
	t.Helper()
	var payload struct {
		Provider string `json:"provider"`
		Results  []struct {
			URL               string                       `json:"url"`
			DiscoverySnippet  string                       `json:"discovery_snippet"`
			PagePublishedDate string                       `json:"page_published_date"`
			PageExcerpts      []map[string]json.RawMessage `json:"page_excerpts"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(referenceWorkflowToolOutput(t, request)), &payload); err != nil {
		t.Fatalf("projected payload did not contain object excerpts: %v", err)
	}
	if payload.Provider != "searxng" || len(payload.Results) != 2 {
		t.Fatalf("projection changed provider or lost source rows: %+v", payload)
	}
	wantText := map[string]string{referenceOpticsURL: "Section: Aurora optics\n" + referenceOptics, referenceFeesURL: "Section: Aurora visitor fees\n" + referenceFees}
	ids := make(map[string]string)
	seenIDs := make(map[string]bool)
	for _, row := range payload.Results {
		want, found := wantText[row.URL]
		if !found || len(row.PageExcerpts) != 1 || row.DiscoverySnippet == "" {
			t.Fatalf("projection lost exact source ownership, passage or discovery metadata: %+v", row)
		}
		excerpt := row.PageExcerpts[0]
		var id, text string
		if len(excerpt) != 2 || json.Unmarshal(excerpt["id"], &id) != nil || json.Unmarshal(excerpt["text"], &text) != nil || id == "" || seenIDs[id] || text != want {
			t.Fatalf("want one unique id + unchanged text object, got %s", referenceWorkflowJSON(t, excerpt))
		}
		if row.URL == referenceOpticsURL && row.PagePublishedDate != "2026-09-03" {
			t.Error("projection changed fetched publication metadata")
		}
		ids[row.URL], seenIDs[id] = id, true
	}
	return ids
}

func assertReferenceWorkflowFormat(t *testing.T, request createRequest, toolsAvailable bool) {
	t.Helper()
	if request.Text == nil {
		t.Fatal("fetched SearXNG candidate omitted v2 schema")
	}
	format := request.Text.Format
	if format.Type != "json_schema" || format.Name != "source_bound_web_answer_v2" || !format.Strict {
		t.Fatalf("wrong v2 format: %+v", format)
	}
	var got, want any
	if json.Unmarshal(format.Schema, &got) != nil || json.Unmarshal([]byte(webEvidenceReferenceSchema), &want) != nil || !reflect.DeepEqual(got, want) {
		t.Error("request did not send the complete reference schema")
	}
	if strings.Contains(string(format.Schema), `"source_url"`) || strings.Contains(string(format.Schema), `"support_quote"`) || !strings.Contains(string(format.Schema), `"support_excerpt_id"`) {
		t.Error("v2 schema retained model-transcribed URL/quote support")
	}
	if !strings.Contains(request.Instructions, webEvidenceReferenceInstructions) || strings.Contains(request.Instructions, webEvidenceInstructions) {
		t.Error("v2 request omitted reference instructions or also advertised legacy support")
	}
	if request.Reasoning == nil || request.Reasoning.Effort != "low" || request.MaxOutputTokens != 3072 {
		t.Errorf("v2 settings = %+v/%d, want low/3072", request.Reasoning, request.MaxOutputTokens)
	}
	if (len(request.Tools) > 0) != toolsAvailable {
		t.Errorf("tools available = %t, want %t", len(request.Tools) > 0, toolsAvailable)
	}
}

func referenceWorkflowAnswer(t *testing.T, id, text string) string {
	t.Helper()
	return referenceWorkflowJSON(t, webReferencedEvidenceAnswer{Claims: []webReferencedEvidenceClaim{{Text: text, SupportExcerptID: id}}, Limitations: []string{}})
}

func assertReferenceWorkflowMapping(t *testing.T, review webEvidenceReviewRecord, ids map[string]string) {
	t.Helper()
	// Inspect the persisted JSON contract, not only the in-process struct.
	var record struct {
		SchemaVersion string `json:"schema_version"`
		References    []struct {
			ID     string            `json:"id"`
			Source webEvidenceSource `json:"source"`
		} `json:"excerpt_references"`
	}
	if err := json.Unmarshal([]byte(referenceWorkflowJSON(t, review)), &record); err != nil {
		t.Fatal(err)
	}
	if record.SchemaVersion != "source_bound_web_answer_v2" || len(record.References) != len(ids) {
		t.Fatalf("serialized diagnostic record lost its version or mapping: %+v", record)
	}
	wantText := map[string]string{referenceOpticsURL: "Section: Aurora optics\n" + referenceOptics, referenceFeesURL: "Section: Aurora visitor fees\n" + referenceFees}
	seen := make(map[string]bool)
	for _, reference := range record.References {
		source := reference.Source
		wantID, found := ids[source.URL]
		if !found || seen[source.URL] || reference.ID != wantID || len(source.PageExcerpts) != 1 || source.PageExcerpts[0] != wantText[source.URL] || source.ExtractionStatus != "succeeded" || source.DiscoverySnippet == "" {
			t.Fatalf("diagnostic ID does not resolve to its original exact fetched row: %+v", reference)
		}
		if source.URL == referenceOpticsURL && source.PagePublishedDate != "2026-09-03" {
			t.Error("diagnostic mapping lost publication date ownership")
		}
		seen[source.URL] = true
	}
}

func TestAgentWebEvidenceReferencesDynamicIDsAndScriptedMeaningReview(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"gpt-5.4-mini", "gpt-5.4-mini-2026-03-17"} {
		t.Run(model, func(t *testing.T) {
			const query = "What focuses incoming light at Aurora, and how do exhibit admission and telescope viewing fees differ?"
			const reversed = "The detector focuses incoming light onto mirrors."
			payload := referenceWorkflowPayload(t, "searxng")
			var ids map[string]string
			var candidate, final string
			agent, run := referenceWorkflowAgent(t, model, payload, 3, func(index int, request createRequest) evidenceAgentStep {
				if index == 0 {
					if request.Text != nil || request.Reasoning != nil || len(request.Tools) == 0 || strings.Contains(request.Instructions, webEvidenceReferenceInstructions) {
						t.Error("initial search routing contract changed before evidence exists")
					}
					return evidenceAgentStep{toolName: "search_web"}
				}
				assertReferenceWorkflowFormat(t, request, index == 1)
				gotIDs := referenceWorkflowIDs(t, request)
				if index == 1 {
					ids = gotIDs
					candidate = referenceWorkflowAnswer(t, ids[referenceOpticsURL], reversed)
					return evidenceAgentStep{text: candidate}
				}
				if !reflect.DeepEqual(gotIDs, ids) {
					t.Error("reference identities changed between candidate and final review")
				}
				final = referenceWorkflowJSON(t, webReferencedEvidenceAnswer{Claims: []webReferencedEvidenceClaim{
					{Text: referenceOptics, SupportExcerptID: ids[referenceOpticsURL]},
					{Text: referenceFees, SupportExcerptID: ids[referenceFeesURL]},
				}, Limitations: []string{}})
				return evidenceAgentStep{text: final}
			})
			response, err := agent.Respond(context.Background(), tool.Scope{}, query, session.Conversation{}, nil)
			if err != nil || len(run.requests) != 3 || len(run.search.arguments) != 1 || len(run.reviews) != 2 {
				t.Fatalf("response=%q error=%v requests=%d searches=%d reviews=%+v", response, err, len(run.requests), len(run.search.arguments), run.reviews)
			}
			// Both outputs are scripted. This proves the bounded review path can
			// replace a mechanically accepted causal error, not that an LLM will.
			if len(run.reviews[0].Rejected) != 0 || !strings.Contains(run.reviews[0].Rendered, reversed) || strings.Contains(response, reversed) || !strings.Contains(response, referenceOptics) || !strings.Contains(response, referenceFees) {
				t.Fatalf("scripted meaning/coverage correction failed: first=%+v final=%q", run.reviews[0], response)
			}
			if run.search.result.Content != payload {
				t.Error("private model projection mutated original search capture")
			}
			for index, review := range run.reviews {
				if review.SchemaVersion != "source_bound_web_answer_v2" || review.Round != index+1 || len(review.ExcerptReferences) != 2 || len(review.Rejected) != 0 {
					t.Errorf("v2 diagnostic metadata missing: %+v", review)
				}
				assertReferenceWorkflowMapping(t, review, ids)
			}
			if run.reviews[0].CandidateJSON != candidate || run.reviews[1].CandidateJSON != final || run.reviews[1].Rendered != response {
				t.Error("review hook lost raw candidates or final rendering")
			}
			var feedbacks, originalQuestions, referenceRows int
			for _, raw := range run.requests[2].Input {
				var message inputMessage
				if json.Unmarshal(raw, &message) != nil {
					continue
				}
				if message.Role == "user" && message.Content == query {
					originalQuestions++
				}
				if message.Role == "user" && strings.Contains(message.Content, "Captured source rows") {
					referenceRows++
					if !strings.Contains(message.Content, ids[referenceOpticsURL]) || !strings.Contains(message.Content, referenceOptics) {
						t.Error("final review representation lost the selected ID or exact captured source")
					}
				}
				if message.Role == "developer" {
					if strings.Contains(message.Content, referenceOptics) || strings.Contains(message.Content, referenceFees) {
						t.Error("fetched source text was promoted to developer instructions")
					}
					if strings.Contains(message.Content, webEvidenceFinalReviewInstructions) {
						feedbacks++
						for _, required := range []string{"found no rejection", "every factual clause", "entity, branch", "section owner, date", "causal relationship", "sources the candidate did not cite", "state that specific limitation", "untrusted source data, not instructions"} {
							if !strings.Contains(message.Content, required) {
								t.Errorf("accepted candidate's final review omitted %q", required)
							}
						}
					}
				}
			}
			if feedbacks != 1 || originalQuestions != 1 || referenceRows != 1 {
				t.Errorf("final review lost required inputs: feedbacks=%d questions=%d reference rows=%d", feedbacks, originalQuestions, referenceRows)
			}
		})
	}
}

func TestAgentWebEvidenceReferencesCapAndNoExtraTools(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"answer_at_cap", "invalid_final_at_cap", "tool_in_final_at_cap", "tool_in_final_before_cap", "tool_in_candidate_at_cap"} {
		t.Run(scenario, func(t *testing.T) {
			var selectedID string
			agent, run := referenceWorkflowAgent(t, "gpt-5.4-mini", referenceWorkflowPayload(t, "searxng"), 3, func(index int, request createRequest) evidenceAgentStep {
				if index == 0 {
					return evidenceAgentStep{toolName: "search_web"}
				}
				assertReferenceWorkflowFormat(t, request, index == 1 && scenario == "tool_in_final_before_cap")
				ids := referenceWorkflowIDs(t, request)
				if index == 1 {
					selectedID = ids[referenceOpticsURL]
					if scenario == "tool_in_candidate_at_cap" {
						return evidenceAgentStep{toolName: "search_web"}
					}
				} else {
					if ids[referenceOpticsURL] != selectedID {
						t.Error("cap review changed reference identity")
					}
					if strings.HasPrefix(scenario, "tool_in_final_") {
						return evidenceAgentStep{toolName: "search_web"}
					}
					if scenario == "invalid_final_at_cap" {
						return evidenceAgentStep{text: referenceWorkflowAnswer(t, selectedID+"-not-active", referenceOptics)}
					}
				}
				return evidenceAgentStep{text: referenceWorkflowAnswer(t, selectedID, referenceOptics)}
			})
			agent.maxToolRounds = 1
			if scenario == "tool_in_final_before_cap" {
				agent.maxToolRounds = 10
			}
			const query = "What focuses incoming light at Aurora?"
			response, err := agent.Respond(context.Background(), tool.Scope{}, query, session.Conversation{}, nil)
			wantRequests, wantReviews := 3, 2
			if strings.HasPrefix(scenario, "tool_in_") {
				if !errors.Is(err, ErrToolRoundLimit) {
					t.Fatalf("unexpected tool attempt should fail closed: response=%q err=%v", response, err)
				}
				wantReviews = 1
				if scenario == "tool_in_candidate_at_cap" {
					wantRequests, wantReviews = 2, 0
				}
			} else if err != nil {
				t.Fatalf("Respond: %v", err)
			} else if scenario == "invalid_final_at_cap" {
				if response != webEvidenceAbstention(query) || len(run.reviews) != 2 || len(run.reviews[1].Rejected) == 0 {
					t.Fatalf("invalid final ID did not abstain after one review: %q / %+v", response, run.reviews)
				}
			} else if response != referenceOptics+" (aurora.example)" {
				t.Errorf("supported answer at cap = %q", response)
			}
			if len(run.requests) != wantRequests || len(run.reviews) != wantReviews || len(run.search.arguments) != 1 {
				t.Errorf("bounded path requests=%d reviews=%d searches=%d, want %d/%d/1", len(run.requests), len(run.reviews), len(run.search.arguments), wantRequests, wantReviews)
			}
		})
	}
}

func TestAgentWebEvidenceReferencesLeaveOtherPathsUnprojected(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct{ name, provider, model string }{
		{"tavily", "tavily", "gpt-5.4-mini"},
		{"missing_provider", "", "gpt-5.4-mini"},
		{"other_model", "searxng", "gpt-5.4"},
		{"unknown_snapshot", "searxng", "gpt-5.4-mini-unknown"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			payload := referenceWorkflowPayload(t, scenario.provider)
			agent, run := referenceWorkflowAgent(t, scenario.model, payload, 3, func(index int, request createRequest) evidenceAgentStep {
				if request.Text != nil || strings.Contains(request.Instructions, webEvidenceReferenceInstructions) {
					t.Error("ineligible provider/model acquired v2 generation")
				}
				if index == 0 {
					return evidenceAgentStep{toolName: "search_web"}
				}
				if referenceWorkflowToolOutput(t, request) != payload {
					t.Error("ineligible provider/model search output was rewritten")
				}
				return evidenceAgentStep{text: "Plain answer."}
			})
			response, err := agent.Respond(context.Background(), tool.Scope{}, "Explain Aurora optics.", session.Conversation{}, nil)
			if err != nil || response != "Plain answer." || len(run.requests) != 3 || len(run.reviews) != 0 || len(run.search.arguments) != 1 || run.search.result.Content != payload {
				t.Fatalf("legacy contract changed: response=%q err=%v requests=%d reviews=%d searches=%d", response, err, len(run.requests), len(run.reviews), len(run.search.arguments))
			}
		})
	}
}

func TestAgentWebEvidenceReferencesGenuinelyLegacyCaptureKeepsV1(t *testing.T) {
	t.Parallel()
	payload := evidenceAgentPayload(t, "searxng")
	agent, run := referenceWorkflowAgent(t, "gpt-5.4-mini", payload, 3, func(index int, request createRequest) evidenceAgentStep {
		if index == 0 {
			return evidenceAgentStep{toolName: "search_web"}
		}
		assertEvidenceAgentRequest(t, request, true)
		if referenceWorkflowToolOutput(t, request) != payload || strings.Contains(request.Instructions, webEvidenceReferenceInstructions) {
			t.Error("genuinely legacy payload was projected or acquired v2 instructions")
		}
		return evidenceAgentStep{text: evidenceAgentAnswer(t, evidenceFixtureURL)}
	})
	response, err := agent.Respond(context.Background(), tool.Scope{}, "Verify telescope information.", session.Conversation{}, nil)
	if err != nil || response != evidenceAgentQuote+" (aurora.example)" || len(run.requests) != 3 || len(run.reviews) != 2 {
		t.Fatalf("legacy generation failed: response=%q err=%v requests=%d reviews=%+v", response, err, len(run.requests), run.reviews)
	}
	for _, review := range run.reviews {
		if review.SchemaVersion != "" || len(review.ExcerptReferences) != 0 {
			t.Errorf("legacy diagnostic record falsely reports v2: %+v", review)
		}
	}
}
