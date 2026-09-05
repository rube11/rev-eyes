package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const evidenceAgentQuote = "Aurora telescope has a 40-centimeter aperture."

type evidenceAgentStep struct{ toolName, text string }

// Every model response and search result is supplied in memory. These tests
// exercise request contracts and mechanical checks, not model entailment.
func evidenceAgentFixture(t *testing.T, model string, search *recordingTool, steps []evidenceAgentStep, requests *[]createRequest) *Agent {
	t.Helper()
	agent := testAgent(t, search, func(w http.ResponseWriter, r *http.Request) {
		var request createRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		*requests = append(*requests, request)
		index := len(*requests) - 1
		if index >= len(steps) {
			t.Errorf("unexpected model request %d beyond the scripted bound", index+1)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		step := steps[index]
		if step.toolName != "" {
			writeJSON(t, w, map[string]any{"output": []any{map[string]any{
				"type": "function_call", "call_id": fmt.Sprintf("call-%d", index), "name": step.toolName, "arguments": `{"value":"Aurora telescope aperture"}`,
			}}})
			return
		}
		writeJSON(t, w, map[string]any{"output": []any{map[string]any{
			"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": step.text}},
		}}})
	})
	agent.model = model
	agent.now = evidenceFixtureNow
	return agent
}

func evidenceAgentPayload(t *testing.T, provider string) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"provider": provider, "results": []webEvidenceSource{{URL: evidenceFixtureURL, Title: "Aurora telescope", Snippet: evidenceAgentQuote}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func evidenceAgentAnswer(t *testing.T, sourceURL string) string {
	t.Helper()
	encoded, err := json.Marshal(webEvidenceAnswer{Claims: []webEvidenceClaim{{Text: evidenceAgentQuote, SourceURL: sourceURL, SupportQuote: evidenceAgentQuote}}, Limitations: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func assertEvidenceAgentRequest(t *testing.T, request createRequest, structured bool) {
	t.Helper()
	if !structured {
		if request.Text != nil {
			t.Errorf("source-bound format activated before eligible review: %#v", request.Text)
		}
		return
	}
	if request.Text == nil {
		t.Fatal("eligible source-bound review omitted its structured format")
	}
	format := request.Text.Format
	if format.Type != "json_schema" || format.Name != "source_bound_web_answer" || !format.Strict {
		t.Errorf("unexpected evidence format contract: %#v", format)
	}
	var got, want any
	if json.Unmarshal(format.Schema, &got) != nil || json.Unmarshal([]byte(webEvidenceSchema), &want) != nil {
		t.Fatal("evidence schema is not valid JSON")
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("review did not send the complete current evidence schema: %s", gotJSON)
	}
	if request.Reasoning == nil || request.Reasoning.Effort != "low" || request.MaxOutputTokens != 3072 {
		t.Errorf("source-bound review settings=%#v/%d, want low/3072", request.Reasoning, request.MaxOutputTokens)
	}
	if !strings.Contains(request.Instructions, webEvidenceInstructions) {
		t.Error("source-bound format lacks evidence validation instructions")
	}
}

func TestAgentWebEvidenceSchemaOnlyAfterActualSearXNGSourcesAndSupportedMini(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"gpt-5.4-mini", "gpt-5.4-mini-2026-03-17"} {
		t.Run(model, func(t *testing.T) {
			search := &recordingTool{name: "search_web", result: tool.Result{Content: evidenceAgentPayload(t, "searxng")}}
			var requests []createRequest
			var reviews []webEvidenceReviewRecord
			candidate := evidenceAgentAnswer(t, evidenceFixtureURL)
			agent := evidenceAgentFixture(t, model, search, []evidenceAgentStep{{toolName: "search_web"}, {text: candidate}, {text: candidate}}, &requests)
			agent.onWebEvidenceReview = func(review webEvidenceReviewRecord) { reviews = append(reviews, review) }
			response, err := agent.Respond(context.Background(), tool.Scope{}, "Verify the telescope aperture.", session.Conversation{}, nil)
			if err != nil || response != evidenceAgentQuote+" (aurora.example)" || len(requests) != 3 || len(search.arguments) != 1 {
				t.Fatalf("response=%q error=%v requests=%d searches=%d", response, err, len(requests), len(search.arguments))
			}
			assertEvidenceAgentRequest(t, requests[0], false)
			if requests[0].Reasoning != nil {
				t.Error("initial routing request must retain omitted reasoning")
			}
			assertEvidenceAgentRequest(t, requests[1], true)
			assertEvidenceAgentRequest(t, requests[2], true)
			if len(requests[1].Tools) == 0 {
				t.Error("first grounded answer before the cap must retain targeted verification tools")
			}
			if len(requests[2].Tools) != 0 {
				t.Error("final semantic review must have tools disabled")
			}
			if len(reviews) != 2 || reviews[0].Round != 1 || reviews[1].Round != 2 || reviews[0].CandidateJSON != candidate || reviews[0].Rendered != response || len(reviews[0].Rejected) != 0 {
				t.Errorf("review diagnostic capture=%#v", reviews)
			}
		})
	}
}

func TestAgentWebEvidenceSchemaDoesNotChangeTavilyNonWebOrOtherModels(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct{ name, model, toolName, payload string }{
		{"tavily", "gpt-5.4-mini", "search_web", evidenceAgentPayload(t, "tavily")},
		{"missing_provider", "gpt-5.4-mini", "search_web", evidenceAgentPayload(t, "")},
		{"no_sources", "gpt-5.4-mini", "search_web", `{"provider":"searxng","results":[]}`},
		{"invalid_sources", "gpt-5.4-mini", "search_web", `{"provider":"searxng","results":[{"url":"javascript:alert(1)","snippet":"Aurora telescope."}]}`},
		{"nonweb_tool", "gpt-5.4-mini", "lookup", evidenceAgentPayload(t, "searxng")},
		{"other_model", "gpt-5.4", "search_web", evidenceAgentPayload(t, "searxng")},
		{"unrecognized_snapshot", "gpt-5.4-mini-unrecognized", "search_web", evidenceAgentPayload(t, "searxng")},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			search := &recordingTool{name: scenario.toolName, result: tool.Result{Content: scenario.payload}}
			steps := []evidenceAgentStep{{toolName: scenario.toolName}, {text: "Final plain text."}}
			if scenario.toolName == "search_web" {
				steps = append(steps, evidenceAgentStep{text: "Final plain text."})
			}
			var requests []createRequest
			agent := evidenceAgentFixture(t, scenario.model, search, steps, &requests)
			agent.onWebEvidenceReview = func(webEvidenceReviewRecord) { t.Error("ineligible response entered evidence rendering") }
			response, err := agent.Respond(context.Background(), tool.Scope{}, "Verify telescope information.", session.Conversation{}, nil)
			if err != nil || response != "Final plain text." || len(requests) != len(steps) {
				t.Fatalf("response=%q error=%v requests=%d", response, err, len(requests))
			}
			for _, request := range requests {
				assertEvidenceAgentRequest(t, request, false)
				for _, raw := range request.Input {
					var message inputMessage
					if json.Unmarshal(raw, &message) == nil && message.Role == "developer" && strings.Contains(message.Content, webEvidenceFinalReviewInstructions) {
						t.Error("legacy provider/model path acquired the new final source review")
					}
				}
			}
		})
	}
}

func TestAgentWebEvidenceReviewIsOnePassAndReportsRejections(t *testing.T) {
	t.Parallel()
	for _, corrected := range []bool{false, true} {
		t.Run(fmt.Sprintf("corrected_%t", corrected), func(t *testing.T) {
			invalid := evidenceAgentAnswer(t, "https://invented.example/telescope")
			second := invalid
			if corrected {
				second = evidenceAgentAnswer(t, evidenceFixtureURL)
			}
			search := &recordingTool{name: "search_web", result: tool.Result{Content: evidenceAgentPayload(t, "searxng")}}
			var requests []createRequest
			var reviews []webEvidenceReviewRecord
			agent := evidenceAgentFixture(t, "gpt-5.4-mini", search, []evidenceAgentStep{{toolName: "search_web"}, {text: invalid}, {text: second}}, &requests)
			agent.maxToolRounds = 10 // Review must stay bounded even with spare rounds.
			agent.onWebEvidenceReview = func(review webEvidenceReviewRecord) { reviews = append(reviews, review) }
			response, err := agent.Respond(context.Background(), tool.Scope{}, "Verify telescope information.", session.Conversation{}, nil)
			want := webEvidenceAbstention("Verify telescope information.")
			if corrected {
				want = evidenceAgentQuote + " (aurora.example)"
			}
			if err != nil || response != want || len(requests) != 3 || len(reviews) != 2 {
				t.Fatalf("response=%q error=%v requests=%d reviews=%d", response, err, len(requests), len(reviews))
			}
			for _, request := range requests[1:] {
				assertEvidenceAgentRequest(t, request, true)
			}
			if len(requests[2].Tools) != 0 {
				t.Error("the single correction must not request more retrieval")
			}
			feedbackCount := 0
			for _, raw := range requests[2].Input {
				var input inputMessage
				if json.Unmarshal(raw, &input) == nil && input.Role == "developer" && strings.Contains(input.Content, "One bounded final review is allowed") {
					feedbackCount++
					if !strings.Contains(input.Content, "source_url is not an exact returned source") {
						t.Error("repair feedback lost the concrete rejection reason")
					}
				}
			}
			if feedbackCount != 1 || reviews[0].Round != 1 || reviews[1].Round != 2 || len(reviews[0].Rejected) == 0 || (len(reviews[1].Rejected) == 0) != corrected {
				t.Errorf("bounded repair metadata: feedback=%d reviews=%#v", feedbackCount, reviews)
			}
			if reviews[0].CandidateJSON != invalid || reviews[1].CandidateJSON != second || reviews[1].Rendered != response {
				t.Error("review hook did not retain both raw candidates and final rendered result")
			}
		})
	}
}

func TestAgentWebEvidenceValidAnswerAtBudgetCapStillGetsFinalReview(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct{ name, final string }{
		{"supported", evidenceAgentAnswer(t, evidenceFixtureURL)},
		{"no_claims", `{"claims":[],"limitations":["partial"]}`},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			search := &recordingTool{name: "search_web", result: tool.Result{Content: evidenceAgentPayload(t, "searxng")}}
			var requests []createRequest
			agent := evidenceAgentFixture(t, "gpt-5.4-mini", search, []evidenceAgentStep{{toolName: "search_web"}, {text: scenario.final}, {text: scenario.final}}, &requests)
			agent.maxToolRounds = 1
			response, err := agent.Respond(context.Background(), tool.Scope{}, "Verify telescope information.", session.Conversation{}, nil)
			want := webEvidenceAbstention("Verify telescope information.")
			if scenario.name == "supported" {
				want = evidenceAgentQuote + " (aurora.example)"
			}
			if err != nil || response != want || len(requests) != 3 || len(search.arguments) != 1 {
				t.Fatalf("response=%q error=%v requests=%d searches=%d", response, err, len(requests), len(search.arguments))
			}
			assertEvidenceAgentRequest(t, requests[1], true)
			assertEvidenceAgentRequest(t, requests[2], true)
			if len(requests[1].Tools) != 0 || len(requests[2].Tools) != 0 || !strings.Contains(requests[1].Instructions, "web-search round budget is exhausted") {
				t.Error("source-bound final cap must still prohibit further tools")
			}
		})
	}
}

func TestAgentWebEvidenceUnexpectedToolAtCapDoesNotExecute(t *testing.T) {
	t.Parallel()
	search := &recordingTool{name: "search_web", result: tool.Result{Content: evidenceAgentPayload(t, "searxng")}}
	var requests []createRequest
	agent := evidenceAgentFixture(t, "gpt-5.4-mini", search, []evidenceAgentStep{{toolName: "search_web"}, {toolName: "search_web"}}, &requests)
	agent.maxToolRounds = 1
	_, err := agent.Respond(context.Background(), tool.Scope{}, "Verify telescope information.", session.Conversation{}, nil)
	if !errors.Is(err, ErrToolRoundLimit) || len(requests) != 2 || len(search.arguments) != 1 {
		t.Fatalf("error=%v requests=%d searches=%d", err, len(requests), len(search.arguments))
	}
	assertEvidenceAgentRequest(t, requests[1], true)
}

func TestAgentWebEvidenceFourSearchRoundsAllowExactlyOneFinalReview(t *testing.T) {
	t.Parallel()
	wrongQuote, err := json.Marshal(webEvidenceAnswer{Claims: []webEvidenceClaim{{
		Text: evidenceAgentQuote, SourceURL: evidenceFixtureURL,
		SupportQuote: "This quote was never returned by the source.",
	}}, Limitations: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	valid := evidenceAgentAnswer(t, evidenceFixtureURL)
	for _, scenario := range []struct {
		name, candidate, correction  string
		corrected, candidateRejected bool
	}{
		{"accepted_candidate", valid, valid, true, false},
		{"corrected_quote", string(wrongQuote), valid, true, true},
		{"wrong_quote_again", string(wrongQuote), string(wrongQuote), false, true},
		{"forged_source_again", evidenceAgentAnswer(t, "https://invented.example/telescope"), evidenceAgentAnswer(t, "https://invented.example/telescope"), false, true},
		{"invalid_json_again", "This is not evidence JSON.", "This is not evidence JSON.", false, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			search := &recordingTool{name: "search_web", result: tool.Result{Content: evidenceAgentPayload(t, "searxng")}}
			steps := []evidenceAgentStep{
				{toolName: "search_web"}, {toolName: "search_web"},
				{toolName: "search_web"}, {toolName: "search_web"},
				{text: scenario.candidate}, {text: scenario.correction},
			}
			var requests []createRequest
			var reviews []webEvidenceReviewRecord
			agent := evidenceAgentFixture(t, "gpt-5.4-mini", search, steps, &requests)
			agent.onWebEvidenceReview = func(review webEvidenceReviewRecord) { reviews = append(reviews, review) }
			response, err := agent.Respond(context.Background(), tool.Scope{}, "Verify telescope information.", session.Conversation{}, nil)
			want := webEvidenceAbstention("Verify telescope information.")
			if scenario.corrected {
				want = evidenceAgentQuote + " (aurora.example)"
			}
			if err != nil || response != want || len(requests) != 6 || len(search.arguments) != 4 || len(reviews) != 2 {
				t.Fatalf("response=%q error=%v requests=%d searches=%d reviews=%d", response, err, len(requests), len(search.arguments), len(reviews))
			}
			for index, request := range requests {
				assertEvidenceAgentRequest(t, request, index > 0)
				if (len(request.Tools) > 0) != (index < 4) {
					t.Errorf("request %d has %d tools; only the original four search rounds may use tools", index, len(request.Tools))
				}
			}
			if reviews[0].Round != 4 || reviews[1].Round != 5 || (len(reviews[0].Rejected) > 0) != scenario.candidateRejected || (len(reviews[1].Rejected) == 0) != scenario.corrected {
				t.Errorf("final correction metadata=%#v", reviews)
			}
			feedbackCount := 0
			for _, raw := range requests[5].Input {
				var message inputMessage
				if json.Unmarshal(raw, &message) == nil && message.Role == "developer" && strings.Contains(message.Content, "One bounded final review is allowed") {
					feedbackCount++
				}
			}
			if feedbackCount != 1 || !strings.Contains(requests[5].Instructions, "final evidence review") {
				t.Errorf("final request lacks exactly one bounded correction: feedback=%d", feedbackCount)
			}
		})
	}
}

func TestAgentWebEvidenceVerificationLookupRetainsSchemaAndExistingBudget(t *testing.T) {
	t.Parallel()
	search := &recordingTool{name: "search_web", result: tool.Result{Content: evidenceAgentPayload(t, "searxng")}}
	var requests []createRequest
	var reviews []webEvidenceReviewRecord
	agent := evidenceAgentFixture(t, "gpt-5.4-mini", search, []evidenceAgentStep{{toolName: "search_web"}, {toolName: "search_web"}, {text: evidenceAgentAnswer(t, evidenceFixtureURL)}, {text: evidenceAgentAnswer(t, evidenceFixtureURL)}}, &requests)
	agent.maxToolRounds = 2
	agent.onWebEvidenceReview = func(review webEvidenceReviewRecord) { reviews = append(reviews, review) }
	response, err := agent.Respond(context.Background(), tool.Scope{}, "Verify telescope information.", session.Conversation{}, nil)
	if err != nil || response != evidenceAgentQuote+" (aurora.example)" || len(requests) != 4 || len(search.arguments) != 2 || len(reviews) != 2 {
		t.Fatalf("response=%q error=%v requests=%d searches=%d reviews=%d", response, err, len(requests), len(search.arguments), len(reviews))
	}
	for _, request := range requests[1:] {
		assertEvidenceAgentRequest(t, request, true)
	}
	if len(requests[1].Tools) == 0 || len(requests[2].Tools) != 0 || len(requests[3].Tools) != 0 || reviews[0].Round != 2 || reviews[1].Round != 3 {
		t.Error("structured verification failed to retain tools before, and remove them at, the existing cap")
	}
	checklists := 0
	for _, raw := range requests[2].Input {
		var input inputMessage
		if json.Unmarshal(raw, &input) == nil && input.Role == "developer" && input.Content == webDraftReviewInstructions {
			checklists++
		}
	}
	if checklists != 0 {
		t.Errorf("grounded lookup must bypass redundant prose draft review: checklists=%d", checklists)
	}
}

func TestAgentWebEvidenceReviewCannotExtendBudgetWithAnotherLookup(t *testing.T) {
	t.Parallel()
	for _, budget := range []int{1, 10} {
		for _, accepted := range []bool{false, true} {
			t.Run(fmt.Sprintf("search_round_budget_%d_candidate_accepted_%t", budget, accepted), func(t *testing.T) {
				search := &recordingTool{name: "search_web", result: tool.Result{Content: evidenceAgentPayload(t, "searxng")}}
				var requests []createRequest
				var reviews []webEvidenceReviewRecord
				candidate := "Invalid evidence JSON."
				if accepted {
					candidate = evidenceAgentAnswer(t, evidenceFixtureURL)
				}
				agent := evidenceAgentFixture(t, "gpt-5.4-mini", search, []evidenceAgentStep{{toolName: "search_web"}, {text: candidate}, {toolName: "search_web"}}, &requests)
				agent.maxToolRounds = budget // Final review cannot reopen tools before or after the search cap.
				agent.onWebEvidenceReview = func(review webEvidenceReviewRecord) { reviews = append(reviews, review) }
				_, err := agent.Respond(context.Background(), tool.Scope{}, "Verify telescope information.", session.Conversation{}, nil)
				if !errors.Is(err, ErrToolRoundLimit) || len(requests) != 3 || len(search.arguments) != 1 || len(reviews) != 1 || (len(reviews[0].Rejected) == 0) != accepted {
					t.Fatalf("error=%v requests=%d searches=%d reviews=%#v", err, len(requests), len(search.arguments), reviews)
				}
				assertEvidenceAgentRequest(t, requests[2], true)
				if len(requests[2].Tools) != 0 {
					t.Error("final review incorrectly reopened tools")
				}
			})
		}
	}
}

func TestAgentWebEvidenceStateResetsBetweenUtterances(t *testing.T) {
	t.Parallel()
	search := &recordingTool{name: "search_web", result: tool.Result{Content: evidenceAgentPayload(t, "searxng")}}
	var requests []createRequest
	agent := evidenceAgentFixture(t, "gpt-5.4-mini", search, []evidenceAgentStep{{toolName: "search_web"}, {text: evidenceAgentAnswer(t, evidenceFixtureURL)}, {text: evidenceAgentAnswer(t, evidenceFixtureURL)}, {text: "Hello again."}}, &requests)
	if _, err := agent.Respond(context.Background(), tool.Scope{}, "Verify telescope information.", session.Conversation{}, nil); err != nil {
		t.Fatal(err)
	}
	response, err := agent.Respond(context.Background(), tool.Scope{}, "Hello", session.Conversation{}, nil)
	if err != nil || response != "Hello again." || len(requests) != 4 {
		t.Fatalf("response=%q error=%v requests=%d", response, err, len(requests))
	}
	assertEvidenceAgentRequest(t, requests[3], false)
	if requests[3].Reasoning != nil || strings.Contains(requests[3].Instructions, webEvidenceInstructions) {
		t.Error("prior utterance leaked source-bound policy into an ordinary new turn")
	}
}
