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

func TestAgentWebSynthesisReasoningOnlyAfterSearchOnSupportedModels(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"gpt-5.4-mini", "gpt-5.4-mini-2026-03-17"} {
		t.Run(model, func(t *testing.T) {
			search := &recordingTool{name: "search_web", result: tool.Result{Content: `{"results":[{"title":"Aurora observation update"}]}`}}
			var requests []map[string]json.RawMessage
			agent := webReasoningTestAgent(t, model, search, []string{"lookup", "search_web", "lookup", "", ""}, &requests)
			if err := agent.registry.Register(&recordingTool{name: "lookup", result: tool.Result{Content: `{"answer":"local context"}`}}); err != nil {
				t.Fatal(err)
			}
			response, err := agent.Respond(context.Background(), tool.Scope{}, "Check the latest Aurora observations.", session.Conversation{}, nil)
			if err != nil || response != "Verified response." {
				t.Fatalf("Respond()=%q error=%v", response, err)
			}
			if len(requests) != 5 {
				t.Fatalf("request count=%d, want planning/tool/synthesis plus one draft-review request", len(requests))
			}
			for index, request := range requests {
				// First planning and the post-lookup pre-search request are unchanged.
				assertWebReasoningRequest(t, request, []string{"", "", "low", "low", "low"}[index])
			}
			if len(search.arguments) != 1 {
				t.Errorf("search calls=%d, want one", len(search.arguments))
			}
		})
	}
}

func TestAgentWebSynthesisReasoningContinuesAfterSearchErrorsAndRetries(t *testing.T) {
	t.Parallel()
	search := &recordingTool{name: "search_web", err: errors.New("search temporarily unavailable")}
	var requests []map[string]json.RawMessage
	agent := webReasoningTestAgent(t, "gpt-5.4-mini", search, []string{"search_web", "search_web", "", ""}, &requests)
	if _, err := agent.Respond(context.Background(), tool.Scope{}, "Verify Aurora telescope news.", session.Conversation{}, nil); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 4 || len(search.arguments) != 2 {
		t.Fatalf("requests=%d search attempts=%d", len(requests), len(search.arguments))
	}
	for index, request := range requests {
		assertWebReasoningRequest(t, request, []string{"", "low", "low", "low"}[index])
	}
}

func TestAgentWebSynthesisReasoningDoesNotChangeOtherModels(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"test-model", "gpt-4.1-mini", "gpt-5.4", "gpt-5.4-mini-unrecognized-snapshot"} {
		t.Run(model, func(t *testing.T) {
			var requests []map[string]json.RawMessage
			agent := webReasoningTestAgent(t, model, &recordingTool{name: "search_web", result: tool.Result{Content: `{"results":[]}`}}, []string{"search_web", "", ""}, &requests)
			if _, err := agent.Respond(context.Background(), tool.Scope{}, "Verify Aurora telescope news.", session.Conversation{}, nil); err != nil {
				t.Fatal(err)
			}
			if len(requests) != 3 {
				t.Fatalf("requests=%d, want search/draft/review requests", len(requests))
			}
			for _, request := range requests {
				assertWebReasoningRequest(t, request, "")
			}
		})
	}
}

func TestAgentWebSynthesisReasoningDoesNotChangeNonWebTurns(t *testing.T) {
	t.Parallel()
	var requests []map[string]json.RawMessage
	agent := webReasoningTestAgent(t, "gpt-5.4-mini", &recordingTool{name: "lookup", result: tool.Result{Content: `{"answer":"local context"}`}}, []string{"lookup", ""}, &requests)
	if _, err := agent.Respond(context.Background(), tool.Scope{}, "Explain what search_web means without calling it.", session.Conversation{}, nil); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests=%d, want two", len(requests))
	}
	for _, request := range requests {
		assertWebReasoningRequest(t, request, "")
	}
}

func TestAgentWebSynthesisReasoningResetsBetweenUtterances(t *testing.T) {
	t.Parallel()
	var requests []map[string]json.RawMessage
	agent := webReasoningTestAgent(t, "gpt-5.4-mini", &recordingTool{name: "search_web", result: tool.Result{Content: `{"results":[]}`}}, []string{"search_web", "", "", ""}, &requests)
	for _, query := range []string{"Search for Aurora telescope news.", "Hello again."} {
		if _, err := agent.Respond(context.Background(), tool.Scope{}, query, session.Conversation{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(requests) != 4 {
		t.Fatalf("requests=%d, want three web requests and one ordinary request", len(requests))
	}
	for index, request := range requests {
		assertWebReasoningRequest(t, request, []string{"", "low", "low", ""}[index])
	}
}

func TestAgentWebDraftReviewReturnsOnlyReviewedTextAndRunsOnce(t *testing.T) {
	t.Parallel()
	var requests []createRequest
	agent := testAgent(t, &recordingTool{name: "search_web", result: tool.Result{Content: `{"results":[{"title":"Aurora observation update"}]}`}}, func(w http.ResponseWriter, r *http.Request) {
		var request createRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		requests = append(requests, request)
		switch len(requests) {
		case 1:
			writeJSON(t, w, map[string]any{"output": []any{map[string]any{
				"type": "function_call", "call_id": "call-search", "name": "search_web", "arguments": `{"value":"Aurora telescope"}`,
			}}})
		case 2, 3:
			text := "Unreviewed draft claim."
			if len(requests) == 3 {
				text = "Reviewed supported answer."
			}
			writeJSON(t, w, map[string]any{"output": []any{map[string]any{
				"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}},
			}}})
		default:
			t.Errorf("draft review repeated unexpectedly: request %d", len(requests))
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	agent.model = "gpt-5.4-mini"
	response, err := agent.Respond(context.Background(), tool.Scope{}, "Verify Aurora telescope news.", session.Conversation{}, nil)
	if err != nil || response != "Reviewed supported answer." || len(requests) != 3 {
		t.Fatalf("Respond()=%q error=%v requests=%d, want reviewed answer after one review", response, err, len(requests))
	}
	reviewRequest := requests[2]
	if reviewRequest.Reasoning == nil || reviewRequest.Reasoning.Effort != "low" || reviewRequest.MaxOutputTokens != 2048 {
		t.Errorf("bounded review settings reasoning=%#v tokens=%d, want low/2048", reviewRequest.Reasoning, reviewRequest.MaxOutputTokens)
	}
	var reviewInstructions int
	for _, raw := range reviewRequest.Input {
		var message inputMessage
		if json.Unmarshal(raw, &message) == nil && message.Role == "developer" && message.Content == webDraftReviewInstructions {
			reviewInstructions++
		}
	}
	encodedInput, _ := json.Marshal(reviewRequest.Input)
	if reviewInstructions != 1 || !strings.Contains(string(encodedInput), "Unreviewed draft claim.") {
		t.Errorf("review input must contain one developer checklist and replayed draft: checklist count=%d", reviewInstructions)
	}
}

func TestAgentWebDraftReviewCanRequestOneTargetedVerification(t *testing.T) {
	t.Parallel()
	search := &recordingTool{name: "search_web", result: tool.Result{Content: `{"results":[{"title":"Aurora observation update"}]}`}}
	var requests []map[string]json.RawMessage
	agent := webReasoningTestAgent(t, "gpt-5.4-mini", search, []string{"search_web", "", "search_web", ""}, &requests)
	if _, err := agent.Respond(context.Background(), tool.Scope{}, "Verify Aurora telescope news.", session.Conversation{}, nil); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 4 || len(search.arguments) != 2 {
		t.Fatalf("requests=%d searches=%d, want initial search, draft, verification, final", len(requests), len(search.arguments))
	}
	for index, request := range requests {
		// A verification search requested by the review does not reset the
		// reviewed stage: both review and subsequent synthesis retain low.
		assertWebReasoningRequest(t, request, []string{"", "low", "low", "low"}[index])
	}
	for _, request := range requests[2:] {
		var input []json.RawMessage
		if err := json.Unmarshal(request["input"], &input); err != nil {
			t.Fatal(err)
		}
		reviewInstructions := 0
		for _, raw := range input {
			var message inputMessage
			if json.Unmarshal(raw, &message) == nil && message.Role == "developer" && message.Content == webDraftReviewInstructions {
				reviewInstructions++
			}
		}
		if reviewInstructions != 1 {
			t.Errorf("targeted verification must not append another review: checklist count=%d", reviewInstructions)
		}
	}
}

func TestAgentWebDraftReviewRespectsExistingRoundBudget(t *testing.T) {
	t.Parallel()
	var requests []map[string]json.RawMessage
	agent := webReasoningTestAgent(t, "gpt-5.4-mini", &recordingTool{name: "search_web", result: tool.Result{Content: `{"results":[]}`}}, []string{"search_web", ""}, &requests)
	agent.maxToolRounds = 1
	if _, err := agent.Respond(context.Background(), tool.Scope{}, "Verify Aurora telescope news.", session.Conversation{}, nil); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatalf("review exceeded existing round budget: requests=%d", len(requests))
	}
	assertWebReasoningRequest(t, requests[0], "")
	assertWebReasoningRequest(t, requests[1], "low")
}

// This checks orchestration, not model truthfulness: a supplied review can
// abstain when evidence is absent, without the agent inventing a source or
// returning the earlier unsupported draft. Live quality still needs grading.
func TestAgentWebDraftReviewPropagatesInsufficientEvidenceAbstention(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name, evidence string
		searchError    error
	}{
		{"empty_results", `{"results":[]}`, nil},
		{"search_error", "", errors.New("search temporarily unavailable")},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			const final = "I couldn't verify tonight's opening hours from the available evidence."
			search := &recordingTool{name: "search_web", result: tool.Result{Content: scenario.evidence}, err: scenario.searchError}
			var requests []createRequest
			agent := testAgent(t, search, func(w http.ResponseWriter, r *http.Request) {
				var request createRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode request: %v", err)
				}
				requests = append(requests, request)
				if len(requests) == 1 {
					writeJSON(t, w, map[string]any{"output": []any{map[string]any{
						"type": "function_call", "call_id": "call-search", "name": "search_web", "arguments": `{"value":"Aurora telescope opening hours"}`,
					}}})
					return
				}
				text := "Unsupported draft: Aurora telescope is open tonight."
				if len(requests) == 3 {
					text = final
				} else if len(requests) > 3 {
					t.Errorf("unexpected repeated review request %d", len(requests))
				}
				writeJSON(t, w, map[string]any{"output": []any{map[string]any{
					"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}},
				}}})
			})
			agent.model = "gpt-5.4-mini"
			response, err := agent.Respond(context.Background(), tool.Scope{}, "Is Aurora telescope open tonight?", session.Conversation{}, nil)
			if err != nil || response != final || len(requests) != 3 {
				t.Fatalf("Respond()=%q error=%v requests=%d, want reviewed abstention", response, err, len(requests))
			}
			var evidencePreserved bool
			for _, raw := range requests[2].Input {
				var output toolOutput
				if json.Unmarshal(raw, &output) != nil || output.Type != "function_call_output" {
					continue
				}
				if scenario.searchError == nil {
					evidencePreserved = output.Output == scenario.evidence
				} else {
					evidencePreserved = strings.Contains(output.Output, scenario.searchError.Error())
				}
			}
			if !evidencePreserved {
				t.Error("review request lost the empty/error evidence needed for abstention")
			}
			assertLiveGlassesResponse(t, response)
		})
	}
}

func TestAgentWebDraftReviewPromptRetainsEvidenceAndResponseLimits(t *testing.T) {
	t.Parallel()
	for _, clause := range []string{"Check every factual clause", "remove unsupported items/clauses", "plainly state what is unverified", "at most 420 characters", "actual source hostnames"} {
		if !strings.Contains(webDraftReviewInstructions, clause) {
			t.Errorf("web draft review lost required evidence/response instruction %q", clause)
		}
	}
}

func TestAgentWebFinalBudgetRoundSynthesizesOrAbstainsWithoutExtraRequests(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct{ name, evidence, final string }{
		{"supported", `{"results":[{"title":"Aurora telescope","url":"https://aurora.example/telescope","snippet":"The Aurora telescope has a 40-centimeter aperture."}]}`, "Aurora telescope has a 40-centimeter aperture (aurora.example)."},
		{"insufficient", `{"results":[]}`, "I couldn't verify the Aurora telescope aperture from the available evidence."},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			search := &recordingTool{name: "search_web", result: tool.Result{Content: scenario.evidence}}
			var requests []map[string]json.RawMessage
			agent := testAgent(t, search, func(w http.ResponseWriter, r *http.Request) {
				var request map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode request: %v", err)
				}
				requests = append(requests, request)
				if len(requests) == 1 {
					writeJSON(t, w, map[string]any{"output": []any{map[string]any{
						"type": "function_call", "call_id": "call-search", "name": "search_web", "arguments": `{"value":"Aurora telescope aperture"}`,
					}}})
					return
				}
				if len(requests) > 2 {
					t.Errorf("unexpected request beyond bounded final synthesis: %d", len(requests))
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				writeJSON(t, w, map[string]any{"output": []any{map[string]any{
					"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": scenario.final}},
				}}})
			})
			agent.model, agent.maxToolRounds = "gpt-5.4-mini", 1
			response, err := agent.Respond(context.Background(), tool.Scope{}, "Verify the Aurora telescope aperture.", session.Conversation{}, nil)
			if err != nil || response != scenario.final || len(requests) != 2 || len(search.arguments) != 1 {
				t.Fatalf("response=%q error=%v requests=%d searches=%d", response, err, len(requests), len(search.arguments))
			}
			assertWebBudgetSynthesisRequest(t, requests[1])
			assertWebReasoningRequest(t, requests[1], "low")
			var input []json.RawMessage
			if err := json.Unmarshal(requests[1]["input"], &input); err != nil {
				t.Fatal(err)
			}
			var foundEvidence bool
			for _, raw := range input {
				var output toolOutput
				if json.Unmarshal(raw, &output) == nil && output.Type == "function_call_output" && output.Output == scenario.evidence {
					foundEvidence = true
				}
			}
			if !foundEvidence {
				t.Error("final bounded synthesis lost its retrieved evidence")
			}
		})
	}
}

func TestAgentWebFinalBudgetRoundStillRejectsUnexpectedToolCalls(t *testing.T) {
	t.Parallel()
	search := &recordingTool{name: "search_web", result: tool.Result{Content: `{"results":[]}`}}
	var requests []map[string]json.RawMessage
	agent := webReasoningTestAgent(t, "gpt-5.4-mini", search, []string{"search_web", "search_web"}, &requests)
	agent.maxToolRounds = 1
	_, err := agent.Respond(context.Background(), tool.Scope{}, "Verify the Aurora telescope aperture.", session.Conversation{}, nil)
	if !errors.Is(err, ErrToolRoundLimit) || len(requests) != 2 || len(search.arguments) != 1 {
		t.Fatalf("error=%v requests=%d searches=%d, want bounded rejection before second tool executes", err, len(requests), len(search.arguments))
	}
	assertWebBudgetSynthesisRequest(t, requests[1])
}

func TestAgentWebFinalBudgetRoundDisablesToolsDuringReview(t *testing.T) {
	t.Parallel()
	var requests []map[string]json.RawMessage
	agent := webReasoningTestAgent(t, "gpt-5.4-mini", &recordingTool{name: "search_web", result: tool.Result{Content: `{"results":[]}`}}, []string{"search_web", "", ""}, &requests)
	agent.maxToolRounds = 2
	if _, err := agent.Respond(context.Background(), tool.Scope{}, "Verify the Aurora telescope aperture.", session.Conversation{}, nil); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 3 {
		t.Fatalf("requests=%d, want search/draft/bounded final review", len(requests))
	}
	assertWebBudgetSynthesisRequest(t, requests[2])
	assertWebReasoningRequest(t, requests[2], "low")
}

func TestAgentWebFinalBudgetPolicyDoesNotChangeNonWebRequests(t *testing.T) {
	t.Parallel()
	var requests []map[string]json.RawMessage
	agent := webReasoningTestAgent(t, "gpt-5.4-mini", &recordingTool{name: "lookup", result: tool.Result{Content: `{"answer":"local context"}`}}, []string{"lookup", ""}, &requests)
	agent.maxToolRounds = 1
	if _, err := agent.Respond(context.Background(), tool.Scope{}, "Use my local preferences.", session.Conversation{}, nil); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatalf("nonweb requests=%d, want unchanged two requests", len(requests))
	}
	if len(requests[1]["tools"]) == 0 || strings.Contains(string(requests[1]["instructions"]), "web-search round budget is exhausted") {
		t.Error("web-only synthesis boundary changed the nonweb request contract")
	}
	assertWebReasoningRequest(t, requests[1], "")
}

func assertWebBudgetSynthesisRequest(t *testing.T, request map[string]json.RawMessage) {
	t.Helper()
	for _, key := range []string{"tools", "tool_choice", "parallel_tool_calls"} {
		if _, exists := request[key]; exists {
			t.Errorf("final web round must omit %s, got %s", key, request[key])
		}
	}
	var instructions string
	if err := json.Unmarshal(request["instructions"], &instructions); err != nil {
		t.Fatal(err)
	}
	for _, clause := range []string{"web-search round budget is exhausted", "using only already returned evidence", "clearly state any unverified constraints"} {
		if !strings.Contains(instructions, clause) {
			t.Errorf("final web round lacks evidence-grounded synthesis instruction %q", clause)
		}
	}
}

func webReasoningTestAgent(t *testing.T, model string, registered tool.Tool, outputTools []string, requests *[]map[string]json.RawMessage) *Agent {
	t.Helper()
	agent := testAgent(t, registered, func(w http.ResponseWriter, r *http.Request) {
		var request map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		index := len(*requests)
		*requests = append(*requests, request)
		if index >= len(outputTools) {
			t.Errorf("unexpected extra model request %d", index)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if name := outputTools[index]; name != "" {
			writeJSON(t, w, map[string]any{"output": []any{map[string]any{
				"type": "function_call", "call_id": fmt.Sprintf("call-%d", index), "name": name, "arguments": `{"value":"Aurora telescope"}`,
			}}})
			return
		}
		writeJSON(t, w, map[string]any{"output": []any{map[string]any{
			"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "Verified response."}},
		}}})
	})
	agent.model = model
	return agent
}

func assertWebReasoningRequest(t *testing.T, request map[string]json.RawMessage, wantEffort string) {
	t.Helper()
	if wantEffort == "" {
		if _, exists := request["reasoning"]; exists {
			t.Errorf("reasoning must remain omitted before web search/for unchanged models: %s", request["reasoning"])
		}
		if _, exists := request["max_output_tokens"]; exists {
			t.Errorf("max_output_tokens must remain omitted before web search/for unchanged models: %s", request["max_output_tokens"])
		}
		return
	}
	var reasoning reasoningConfig
	if err := json.Unmarshal(request["reasoning"], &reasoning); err != nil || reasoning.Effort != wantEffort {
		t.Errorf("web reasoning=%s, want %s; decode error=%v", request["reasoning"], wantEffort, err)
	}
	wantTokenLimit := 2048
	var tokenLimit int
	if err := json.Unmarshal(request["max_output_tokens"], &tokenLimit); err != nil || tokenLimit != wantTokenLimit {
		t.Errorf("web %s reasoning max_output_tokens=%s, want %d; decode error=%v", wantEffort, request["max_output_tokens"], wantTokenLimit, err)
	}
}
