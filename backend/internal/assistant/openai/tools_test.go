package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/assistant"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const testSchema = `{
  "type": "object",
  "properties": {
    "value": {"type": "string"}
  },
  "required": ["value"],
  "additionalProperties": false
}`

type recordingTool struct {
	mu        sync.Mutex
	name      string
	scopes    []tool.Scope
	arguments []string
	result    tool.Result
	err       error
	mutating  bool
	started   chan<- struct{}
	release   <-chan struct{}
}

func (t *recordingTool) Spec() tool.Spec {
	name := t.name
	if name == "" {
		name = "lookup"
	}
	return tool.Spec{
		Name:        name,
		Description: "Look up a value.",
		Parameters:  json.RawMessage(testSchema),
		ReadOnly:    !t.mutating,
	}
}

func (t *recordingTool) Execute(
	ctx context.Context,
	scope tool.Scope,
	arguments json.RawMessage,
) (tool.Result, error) {
	t.mu.Lock()
	t.scopes = append(t.scopes, scope)
	t.arguments = append(t.arguments, string(arguments))
	result, err := t.result, t.err
	t.mu.Unlock()

	if t.started != nil {
		t.started <- struct{}{}
	}
	if t.release != nil {
		select {
		case <-t.release:
		case <-ctx.Done():
			return tool.Result{}, ctx.Err()
		}
	}

	return result, err
}

type toolRunnerFunc func(context.Context, tool.Scope, assistant.ResponseContext) (assistant.ToolRunResult, error)

func (f toolRunnerFunc) Run(ctx context.Context, scope tool.Scope, state assistant.ResponseContext) (assistant.ToolRunResult, error) {
	return f(ctx, scope, state)
}

type fixedToolClassifier struct{ names []string }

func (f fixedToolClassifier) Select(_ context.Context, state assistant.ToolState, _ []tool.Spec) ([]string, error) {
	if len(state.Results) > 0 {
		return nil, nil
	}
	return f.names, nil
}

type fixedToolArguments map[string]assistant.PreparedToolArguments

func (f fixedToolArguments) Build(_ context.Context, _ assistant.ToolState, _ []tool.Spec) (map[string]assistant.PreparedToolArguments, error) {
	return f, nil
}

func attachTestWorkflow(t *testing.T, agent *Agent, registered tool.Tool, args string) {
	t.Helper()
	registry := tool.NewRegistry()
	if err := registry.Register(registered); err != nil {
		t.Fatal(err)
	}
	name := registered.Spec().Name
	workflow, err := assistant.NewToolWorkflow(registry, fixedToolClassifier{names: []string{name}}, fixedToolArguments{name: {Arguments: json.RawMessage(args)}})
	if err != nil {
		t.Fatal(err)
	}
	agent.workflow = workflow
}

func TestAgentComposesOneFinalResponseAfterTools(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "tool failure"}[failure], func(t *testing.T) {
			lookup := &recordingTool{result: tool.Result{Content: `{"answer":"found"}`}}
			if failure {
				lookup.err = errors.New("location unavailable")
			}
			requests := 0
			scope := tool.Scope{UserID: "user", SessionID: "session"}
			agent := testAgent(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				var request map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				if _, ok := request["tools"]; ok {
					t.Fatal("final model was given tools")
				}
				if len(lookup.scopes) != 1 || lookup.scopes[0] != scope {
					t.Fatal("tools must run first with trusted scope")
				}
				want := "found"
				if failure {
					want = "location unavailable"
				}
				if !strings.Contains(string(request["input"]), want) {
					t.Fatalf("missing tool result: %s", request["input"])
				}
				writeJSON(t, w, map[string]any{"output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "A final response."}}}}})
			})
			attachTestWorkflow(t, agent, lookup, `{"value":"cafes"}`)
			response, err := agent.Respond(context.Background(), scope, "What's nearby?", session.Conversation{}, nil)
			if err != nil || response != "A final response." || requests != 1 {
				t.Fatalf("response=%q requests=%d err=%v", response, requests, err)
			}
		})
	}
}

func TestAgentPreservesActualProposalStatus(t *testing.T) {
	for _, ending := range []string{"success", "api error", "empty", "unexpected tool", "proposal error"} {
		t.Run(ending, func(t *testing.T) {
			proposal := &recordingTool{name: "propose_task", mutating: true, result: tool.Result{Content: `{"status":"proposed"}`}}
			if ending == "proposal error" {
				proposal.err = errors.New("unavailable")
			}
			agent := testAgent(t, func(w http.ResponseWriter, r *http.Request) {
				switch ending {
				case "api error":
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
				case "empty":
					writeJSON(t, w, map[string]any{"output": []any{}})
				case "unexpected tool":
					writeJSON(t, w, map[string]any{"output": []any{map[string]any{"type": "function_call", "name": "propose_task", "call_id": "bad", "arguments": "{}"}}})
				default:
					writeJSON(t, w, map[string]any{"output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "Should I save it?"}}}}})
				}
			})
			attachTestWorkflow(t, agent, proposal, `{"value":"tomorrow"}`)
			result, err := agent.RespondWithResult(context.Background(), tool.Scope{}, assistant.ActionRespond, "Remind me tomorrow", session.Conversation{}, nil)
			if result.ProposalCreated != (ending != "proposal error") {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if result.ProposalCreated && !reflect.DeepEqual(result.ProposalKinds, []assistant.ProposalKind{assistant.ProposalTask}) {
				t.Fatalf("proposal kinds=%v", result.ProposalKinds)
			}
			wantError := ending == "api error" || ending == "empty" || ending == "unexpected tool"
			if (err != nil) != wantError {
				t.Fatalf("err=%v want error=%v", err, wantError)
			}
			if len(proposal.scopes) != 1 {
				t.Fatalf("executed %d proposals", len(proposal.scopes))
			}
		})
	}
}

func TestAgentUsesOneSharedContextAndSkipsWorkflowForMemoryReview(t *testing.T) {
	for _, review := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "memory review"}[review], func(t *testing.T) {
			conversation := session.Conversation{Profile: "User profile: prefers quiet cafes", Summary: "Planning lunch", Messages: []session.Message{{Speaker: session.SpeakerUser, Text: "Near work please"}}}
			scope := tool.Scope{UserID: "private-id", TimeZone: "America/Los_Angeles", AlwaysRespond: true, MemoryReview: review}
			now := time.Date(2026, 9, 19, 18, 0, 0, 0, time.UTC)
			want, err := assistant.NewResponseContext(scope, "Find lunch", conversation, nil, now)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			agent := testAgent(t, func(w http.ResponseWriter, r *http.Request) {
				var request createRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				input, err := responseInput(want, nil)
				if err != nil || !reflect.DeepEqual(input, request.Input) || request.Instructions != responseInstructions(want) {
					t.Fatal("final model received different context")
				}
				writeJSON(t, w, map[string]any{"output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "Okay."}}}}})
			})
			agent.now = func() time.Time { return now }
			agent.workflow = toolRunnerFunc(func(_ context.Context, gotScope tool.Scope, got assistant.ResponseContext) (assistant.ToolRunResult, error) {
				calls++
				if !reflect.DeepEqual(got, want) || gotScope != scope {
					t.Fatalf("workflow context=%+v", got)
				}
				return assistant.ToolRunResult{}, nil
			})
			if _, err := agent.Respond(context.Background(), scope, "Find lunch", conversation, nil); err != nil {
				t.Fatal(err)
			}
			if calls != map[bool]int{true: 0, false: 1}[review] {
				t.Fatalf("workflow calls=%d", calls)
			}
		})
	}
}
