package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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
	scopes    []tool.Scope
	arguments []string
	result    tool.Result
	err       error
	mutating  bool
	started   chan<- struct{}
	release   <-chan struct{}
}

func (t *recordingTool) Spec() tool.Spec {
	return tool.Spec{
		Name:        "lookup",
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestAgentReturnsText(t *testing.T) {
	t.Parallel()

	var request createRequest
	agent := testAgent(t, &recordingTool{}, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writeJSON(t, w, map[string]any{
			"output": []any{
				map[string]any{
					"type": "message",
					"content": []any{
						map[string]any{
							"type": "output_text",
							"text": "  A concise response.  ",
						},
					},
				},
			},
		})
	})

	response, err := agent.Respond(context.Background(), tool.Scope{}, "  hello  ")
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}
	if response != "A concise response." {
		t.Fatalf("Respond() = %q", response)
	}
	if request.Model != "test-model" || len(request.Tools) != 1 {
		t.Fatalf("request = %#v", request)
	}
	if !request.Tools[0].Strict || request.Store {
		t.Fatalf("tool strict = %v, store = %v", request.Tools[0].Strict, request.Store)
	}
	if len(request.Include) != 1 ||
		request.Include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %#v", request.Include)
	}
}

func TestAgentExecutesAndReplaysToolCalls(t *testing.T) {
	t.Parallel()

	lookup := &recordingTool{result: tool.Result{Content: `{"answer":"found"}`}}
	var requests []createRequest
	var mu sync.Mutex

	agent := testAgent(t, lookup, func(w http.ResponseWriter, r *http.Request) {
		var request createRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}

		mu.Lock()
		requests = append(requests, request)
		number := len(requests)
		mu.Unlock()

		if number == 1 {
			writeJSON(t, w, map[string]any{
				"output": []any{
					map[string]any{
						"type":      "function_call",
						"call_id":   "call-1",
						"name":      "lookup",
						"arguments": `{"value":"cafes"}`,
					},
					map[string]any{
						"type":      "function_call",
						"call_id":   "call-2",
						"name":      "lookup",
						"arguments": `{"value":"parks"}`,
					},
				},
			})
			return
		}

		writeJSON(t, w, map[string]any{
			"output": []any{
				map[string]any{
					"type": "message",
					"content": []any{
						map[string]any{
							"type": "output_text",
							"text": "I found both.",
						},
					},
				},
			},
		})
	})

	scope := tool.Scope{UserID: "user-123", SessionID: "session-456"}
	response, err := agent.Respond(context.Background(), scope, "What is nearby?")
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}
	if response != "I found both." {
		t.Fatalf("Respond() = %q", response)
	}

	lookup.mu.Lock()
	defer lookup.mu.Unlock()
	if len(lookup.scopes) != 2 ||
		lookup.scopes[0] != scope ||
		lookup.scopes[1] != scope {
		t.Fatalf("tool scopes = %#v", lookup.scopes)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || len(requests[1].Input) != 5 {
		t.Fatalf("requests = %#v", requests)
	}
	for index, callID := range []string{"call-1", "call-2"} {
		var output toolOutput
		if err := json.Unmarshal(requests[1].Input[index+3], &output); err != nil {
			t.Fatalf("decode tool output: %v", err)
		}
		if output.CallID != callID || output.Output != `{"answer":"found"}` {
			t.Fatalf("tool output = %#v", output)
		}
	}
}

func TestAgentExecutesReadOnlyToolCallsConcurrently(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 2)
	release := make(chan struct{})
	lookup := &recordingTool{
		result:  tool.Result{Content: `{"answer":"found"}`},
		started: started,
		release: release,
	}
	agent := testAgent(t, lookup, func(http.ResponseWriter, *http.Request) {})
	calls := []toolCall{
		{CallID: "call-1", Name: "lookup", Arguments: json.RawMessage(`{"value":"cafes"}`)},
		{CallID: "call-2", Name: "lookup", Arguments: json.RawMessage(`{"value":"parks"}`)},
	}

	type executionResult struct {
		outputs []json.RawMessage
		err     error
	}
	done := make(chan executionResult, 1)
	go func() {
		outputs, err := agent.executeCalls(context.Background(), tool.Scope{}, calls)
		done <- executionResult{outputs: outputs, err: err}
	}()

	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for range calls {
		select {
		case <-started:
		case <-timer.C:
			close(release)
			t.Fatal("tool calls did not start concurrently")
		}
	}
	close(release)

	result := <-done
	if result.err != nil {
		t.Fatalf("executeCalls() error = %v", result.err)
	}
	for index, call := range calls {
		var output toolOutput
		if err := json.Unmarshal(result.outputs[index], &output); err != nil {
			t.Fatalf("decode output: %v", err)
		}
		if output.CallID != call.CallID {
			t.Fatalf("output call ID = %q, want %q", output.CallID, call.CallID)
		}
	}
}

func TestAgentDoesNotParallelizeMutatingToolCalls(t *testing.T) {
	t.Parallel()

	lookup := &recordingTool{mutating: true}
	agent := testAgent(t, lookup, func(http.ResponseWriter, *http.Request) {})
	calls := []toolCall{
		{CallID: "call-1", Name: "lookup"},
		{CallID: "call-2", Name: "lookup"},
	}

	if agent.canRunInParallel(calls) {
		t.Fatal("canRunInParallel() = true for mutating tool")
	}
}

func TestAgentReturnsToolErrorsToModel(t *testing.T) {
	t.Parallel()

	lookup := &recordingTool{err: errors.New("location unavailable")}
	requestNumber := 0
	agent := testAgent(t, lookup, func(w http.ResponseWriter, r *http.Request) {
		requestNumber++
		var request createRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}

		if requestNumber == 1 {
			writeJSON(t, w, map[string]any{
				"output": []any{
					map[string]any{
						"type":      "function_call",
						"call_id":   "call-error",
						"name":      "lookup",
						"arguments": `{"value":"location"}`,
					},
				},
			})
			return
		}

		var output toolOutput
		if err := json.Unmarshal(request.Input[len(request.Input)-1], &output); err != nil {
			t.Errorf("decode tool output: %v", err)
		}
		if !strings.Contains(output.Output, "location unavailable") {
			t.Errorf("tool output = %q", output.Output)
		}
		writeJSON(t, w, map[string]any{
			"output": []any{
				map[string]any{
					"type": "message",
					"content": []any{
						map[string]any{
							"type": "output_text",
							"text": "I cannot access your location yet.",
						},
					},
				},
			},
		})
	})

	response, err := agent.Respond(context.Background(), tool.Scope{}, "Where am I?")
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}
	if response != "I cannot access your location yet." {
		t.Fatalf("Respond() = %q", response)
	}
}

func TestAgentStopsAtRoundLimit(t *testing.T) {
	t.Parallel()

	requestNumber := 0
	agent := testAgent(t, &recordingTool{}, func(w http.ResponseWriter, _ *http.Request) {
		requestNumber++
		writeJSON(t, w, map[string]any{
			"output": []any{
				map[string]any{
					"type":      "function_call",
					"call_id":   fmt.Sprintf("call-%d", requestNumber),
					"name":      "lookup",
					"arguments": `{"value":"again"}`,
				},
			},
		})
	})
	agent.maxToolRounds = 2

	if _, err := agent.Respond(context.Background(), tool.Scope{}, "keep looking"); !errors.Is(err, ErrToolRoundLimit) {
		t.Fatalf("Respond() error = %v", err)
	}
	if requestNumber != 3 {
		t.Fatalf("request count = %d", requestNumber)
	}
}

func TestAgentReportsAPIError(t *testing.T) {
	t.Parallel()

	agent := testAgent(t, nil, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(t, w, map[string]any{
			"error": map[string]string{"message": "invalid request"},
		})
	})

	_, err := agent.Respond(context.Background(), tool.Scope{}, "hello")
	if err == nil || !strings.Contains(err.Error(), "invalid request") {
		t.Fatalf("Respond() error = %v", err)
	}
}

func testAgent(
	t *testing.T,
	registered tool.Tool,
	handler http.HandlerFunc,
) *Agent {
	t.Helper()

	registry := tool.NewRegistry()
	if registered != nil {
		if err := registry.Register(registered); err != nil {
			t.Fatalf("Register() error = %v", err)
		}
	}
	executor, err := tool.NewExecutor(registry)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	agent, err := NewAgent("test-key", "test-model", registry, executor)
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}

	agent.client = &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			return recorder.Result(), nil
		}),
	}
	return agent
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}
