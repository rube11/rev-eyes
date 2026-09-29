package tooling

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/assistant"
	"github.com/rube11/rev-eyes/backend/internal/assistant/openai/responses"
	"github.com/rube11/rev-eyes/backend/internal/tool"
	"github.com/rube11/rev-eyes/backend/internal/tool/websearch"
)

const testSchema = `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`

type observedRequest struct {
	Instructions string            `json:"instructions"`
	Model        string            `json:"model"`
	Input        []json.RawMessage `json:"input"`
	Text         map[string]any    `json:"text"`
	Tools        []json.RawMessage `json:"tools"`
}

func TestArgumentBuilderUsesFixedSchemaAndCompleteContext(t *testing.T) {
	state := assistant.ToolState{
		Context: assistant.ResponseContext{Query: "Find a quiet cafe nearby", Profile: "Prefers outdoor seating", Summary: "Meeting Maya", CurrentLocalTime: "2026-09-19T11:00:00-07:00", TimeZone: "America/Los_Angeles"},
		Results: []assistant.ToolObservation{{Name: "get_current_location", Content: `{"city":"Las Vegas"}`}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request observedRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request.Tools) > 0 || request.Model != "argument-model" {
			t.Fatalf("request=%+v", request)
		}
		for _, example := range []string{"under 400 characters", "maximum is 1500", "JT4 Las Vegas company", "Intbox AI enterprise Henderson", "user biography", "Seattle weather forecast today", "quiet cafes Seattle", "Bluebird Museum opening hours", "New York news today", "Never guess an official domain", "Only one search call"} {
			if !strings.Contains(request.Instructions, example) {
				t.Errorf("missing search guidance %q", example)
			}
		}
		format := request.Text["format"].(map[string]any)
		schema := format["schema"].(map[string]any)
		properties := schema["properties"].(map[string]any)
		if len(properties) != 1 || properties["search_web"] == nil || schema["additionalProperties"] != false || format["strict"] != true {
			t.Fatalf("schema=%+v", schema)
		}
		fields := properties["search_web"].(map[string]any)["properties"].(map[string]any)
		if len(fields) != 1 || fields["arguments"].(map[string]any)["type"] != "object" {
			t.Fatalf("builder has veto field or nullable arguments: %+v", fields)
		}
		var input struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(request.Input[0], &input); err != nil {
			t.Fatal(err)
		}
		var received assistant.ToolState
		if err := json.Unmarshal([]byte(input.Content), &received); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(received, state) {
			t.Fatalf("state=%+v", received)
		}
		writeJSON(t, w, `{"search_web":{"arguments":{"query":"quiet outdoor cafes Las Vegas"}}}`)
	}))
	defer server.Close()
	builder, err := NewArgumentBuilder("test-key", "argument-model", responses.Config{HTTPClient: server.Client(), Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	result, err := builder.Build(context.Background(), state, []tool.Spec{{Name: "search_web", Parameters: json.RawMessage(testSchema)}})
	if err != nil || !strings.Contains(string(result["search_web"].Arguments), "Las Vegas") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestArgumentBuilderRejectsDifferentSelectionAndInvalidOutput(t *testing.T) {
	for _, output := range []string{
		`{"search_web":{"arguments":null}}`,
		`{"search_web":{}}`,
		`{"search_web":{"arguments":{},"missing_information":"Need a neighborhood"}}`,
		`{"propose_task":{"arguments":{},"missing_information":""}}`,
		`{"search_web":{"arguments":{},"missing_information":""},"propose_task":{"arguments":{},"missing_information":""}}`,
		`{"search_web":{"arguments":{},"missing_information":"","action":"propose_task"}}`,
		`{"search_web":{"arguments":{},"missing_information":""}} {}`,
		`null`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeJSON(t, w, output) }))
		builder, _ := NewArgumentBuilder("test-key", "argument-model", responses.Config{HTTPClient: server.Client(), Endpoint: server.URL})
		_, err := builder.Build(context.Background(), assistant.ToolState{}, []tool.Spec{{Name: "search_web", Parameters: json.RawMessage(testSchema)}})
		server.Close()
		if err == nil {
			t.Fatalf("accepted %s", output)
		}
	}
}

func TestArgumentBuilderRetriesMalformedOutputBeforeReturningArguments(t *testing.T) {
	requests := 0
	valid := `{"search_web":{"arguments":{"query":"cafes"}}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		output := valid
		if requests == 1 {
			output += "\n" + valid
		}
		writeJSON(t, w, output)
	}))
	defer server.Close()
	builder, _ := NewArgumentBuilder("test-key", "argument-model", responses.Config{HTTPClient: server.Client(), Endpoint: server.URL})
	result, err := builder.Build(context.Background(), assistant.ToolState{}, []tool.Spec{{Name: "search_web", Parameters: json.RawMessage(testSchema)}})
	if err != nil || requests != 2 || string(result["search_web"].Arguments) != `{"query":"cafes"}` {
		t.Fatalf("requests=%d result=%+v err=%v", requests, result, err)
	}
}

func TestArgumentBuilderValidatesSearchBeforeReturningIt(t *testing.T) {
	search, _ := websearch.New("unused")
	valid := `{"search_web":{"arguments":{"query":"JT4 Las Vegas engineering careers","mode":"research","topic":"general","recency":"none","include_domains":[]}}}`
	for _, invalid := range []string{
		`{"search_web":{"arguments":{"query":"` + strings.Repeat("a", 1501) + `"}}}`,
		`{"search_web":{"arguments":{"query":"JT4","include_domains":["https://example.com/path"]}}}`,
	} {
		for _, repair := range []bool{true, false} {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				output := invalid
				if repair && requests == 2 {
					output = valid
				}
				writeJSON(t, w, output)
			}))
			builder, _ := NewArgumentBuilder("test-key", "argument-model", responses.Config{HTTPClient: server.Client(), Endpoint: server.URL})
			result, err := builder.Build(context.Background(), assistant.ToolState{Context: assistant.ResponseContext{Query: "Research JT4 and how my projects fit"}}, []tool.Spec{search.Spec()})
			server.Close()
			if requests != 2 || (repair && err != nil) || (!repair && err == nil) {
				t.Errorf("repair=%t requests=%d error=%v", repair, requests, err)
			}
			if repair && !strings.Contains(string(result["search_web"].Arguments), "JT4 Las Vegas engineering careers") {
				t.Error("invalid arguments escaped preparation")
			}
		}
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, output string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"output": []any{map[string]any{
		"type": "message", "phase": "final_answer",
		"content": []any{map[string]any{"type": "output_text", "text": output}},
	}}}); err != nil {
		t.Fatal(err)
	}
}
