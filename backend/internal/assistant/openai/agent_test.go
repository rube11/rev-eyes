package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/assistant"
	"github.com/rube11/rev-eyes/backend/internal/assistant/openai/responses"
	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/speech"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

type inputMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type createRequest struct {
	Model           string            `json:"model"`
	Instructions    string            `json:"instructions"`
	Input           []json.RawMessage `json:"input"`
	Text            map[string]any    `json:"text,omitempty"`
	MaxOutputTokens int               `json:"max_output_tokens,omitempty"`
	Store           bool              `json:"store"`
	Include         []string          `json:"include,omitempty"`
}

// Capture forbidden fields too, so tests detect accidental tool exposure.
type observedRequest struct {
	createRequest
	Tools             []json.RawMessage `json:"tools"`
	ParallelToolCalls bool              `json:"parallel_tool_calls"`
}

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestAgentReturnsText(t *testing.T) {
	t.Parallel()

	var request observedRequest
	agent := testAgent(t, func(w http.ResponseWriter, r *http.Request) {
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

	response, err := agent.Respond(
		context.Background(),
		tool.Scope{},
		"  hello  ",
		session.Conversation{},
		nil,
	)
	if err != nil {
		t.Fatalf("Respond() error = %v", err)
	}
	if response != "A concise response." {
		t.Fatalf("Respond() = %q", response)
	}
	if request.Model != "test-model" || len(request.Tools) != 0 {
		t.Fatalf("request = %#v", request)
	}
	if !strings.Contains(request.Instructions, "You only compose the final response") {
		t.Fatalf("instructions = %q", request.Instructions)
	}
	if !strings.Contains(request.Instructions, "# Eyes tonality guide") ||
		!strings.Contains(request.Instructions, "Have a sense of humor") ||
		!strings.Contains(request.Instructions, "Suppress humor entirely") {
		t.Fatalf("instructions missing embedded tonality guide = %q", request.Instructions)
	}
	if !strings.Contains(request.Instructions, "pending proposal") ||
		!strings.Contains(request.Instructions, "could not verify") {
		t.Fatalf("tool result instructions = %q", request.Instructions)
	}
	if !strings.Contains(request.Instructions, "the glasses can render each result separately") {
		t.Fatalf("instructions = %q", request.Instructions)
	}
	if !strings.Contains(request.Instructions, "Never output more than three numbered lines") ||
		!strings.Contains(request.Instructions, "Group shopping and grocery items") {
		t.Fatalf("list instructions = %q", request.Instructions)
	}
	if !strings.Contains(request.Instructions, "meaningful state transition") ||
		!strings.Contains(request.Instructions, "at most one timely next step") {
		t.Fatalf("transition instructions = %q", request.Instructions)
	}
	if !strings.Contains(request.Instructions, "within 420 characters") {
		t.Fatalf("response length instructions = %q", request.Instructions)
	}
	if request.Store {
		t.Fatal("response must not be stored")
	}
	if len(request.Include) != 1 ||
		request.Include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %#v", request.Include)
	}
}

func TestSuggestTipUsesSharedContextToolsAndTipInstructions(t *testing.T) {
	conversation := session.Conversation{
		Profile:  "User profile: studies best in quiet places.",
		Summary:  "The user is preparing for exams.",
		Messages: []session.Message{{Speaker: session.SpeakerUser, Text: "I lose focus after twenty minutes."}},
	}
	memories := []memory.Card{{Title: "Study goal", Summary: "Finish the biology review tonight."}}
	scope := tool.Scope{TimeZone: "America/Los_Angeles", Speech: &speech.Utterance{
		Text: "Any tip for staying focused?", Segments: []speech.Segment{{Role: speech.Other, Text: "Any tip for staying focused?"}},
	}}
	now := time.Date(2026, 9, 21, 18, 0, 0, 0, time.UTC)
	want, err := assistant.NewResponseContext(scope, "Any tip for staying focused?", conversation, memories, now)
	if err != nil {
		t.Fatal(err)
	}
	want.Action = assistant.ActionSuggestTip

	var request createRequest
	agent := testAgent(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		writeJSON(t, w, map[string]any{"output": []any{map[string]any{
			"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "Put your phone away for one focused twenty-minute block."}},
		}}})
	})
	agent.now = func() time.Time { return now }
	agent.workflow = toolRunnerFunc(func(_ context.Context, gotScope tool.Scope, got assistant.ResponseContext) (assistant.ToolRunResult, error) {
		if gotScope != scope || !reflect.DeepEqual(got, want) {
			t.Fatalf("tool context = %#v, want %#v", got, want)
		}
		return assistant.ToolRunResult{}, nil
	})

	result, err := agent.RespondWithResult(context.Background(), scope, assistant.ActionSuggestTip, "Any tip for staying focused?", conversation, memories)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text == "" || !strings.Contains(request.Instructions, "classified suggest_tip") ||
		!strings.Contains(request.Instructions, "# Eyes tonality guide") {
		t.Fatalf("result=%#v instructions=%q", result, request.Instructions)
	}
	encodedInput, _ := json.Marshal(request.Input)
	if !strings.Contains(string(encodedInput), "speech_attribution") || !strings.Contains(string(encodedInput), `\"speaker_role\":\"other\"`) {
		t.Fatal("final composer did not receive speaker attribution")
	}
	input, err := responseInput(want, nil)
	if err != nil || !reflect.DeepEqual(request.Input, input) {
		t.Fatal("suggest_tip did not use the shared response context")
	}
}

func TestAgentIncludesCurrentLocalTime(t *testing.T) {
	t.Parallel()

	var request createRequest
	agent := testAgent(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writeJSON(t, w, map[string]any{
			"output": []any{map[string]any{
				"type": "message",
				"content": []any{map[string]any{
					"type": "output_text",
					"text": "Done.",
				}},
			}},
		})
	})
	agent.now = func() time.Time {
		return time.Date(2026, time.July, 21, 18, 30, 0, 0, time.UTC)
	}

	if _, err := agent.Respond(
		context.Background(),
		tool.Scope{TimeZone: "America/Los_Angeles"},
		"I need to call the dentist tomorrow morning.",
		session.Conversation{},
		nil,
	); err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	want := "Current local date and time: 2026-07-21T11:30:00-07:00 (America/Los_Angeles)."
	if !strings.Contains(request.Instructions, want) {
		t.Fatalf("instructions = %q", request.Instructions)
	}
}

func TestAgentIncludesRelevantMemoriesAsUserData(t *testing.T) {
	t.Parallel()

	var request createRequest
	agent := testAgent(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writeJSON(t, w, map[string]any{
			"output": []any{
				map[string]any{
					"type": "message",
					"content": []any{
						map[string]any{"type": "output_text", "text": "Maya."},
					},
				},
			},
		})
	})

	memories := []memory.Card{{
		Topics:  []memory.Topic{memory.TopicWork},
		Kind:    memory.KindRelationship,
		Title:   "Maya is my boss",
		Summary: "Maya is the user's boss.",
	}}
	if _, err := agent.Respond(
		context.Background(),
		tool.Scope{},
		"Who is my boss?",
		session.Conversation{},
		memories,
	); err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	if len(request.Input) != 2 {
		t.Fatalf("input count = %d, want 2", len(request.Input))
	}
	var memoryInput, queryInput inputMessage
	if err := json.Unmarshal(request.Input[0], &memoryInput); err != nil {
		t.Fatalf("decode memory input: %v", err)
	}
	if err := json.Unmarshal(request.Input[1], &queryInput); err != nil {
		t.Fatalf("decode query input: %v", err)
	}
	if memoryInput.Role != "user" ||
		!strings.Contains(memoryInput.Content, memories[0].Summary) {
		t.Fatalf("memory input = %#v", memoryInput)
	}
	if queryInput.Role != "user" || queryInput.Content != "Who is my boss?" {
		t.Fatalf("query input = %#v", queryInput)
	}
}

func TestAgentIncludesConversationBeforeCurrentQuery(t *testing.T) {
	t.Parallel()

	var request createRequest
	agent := testAgent(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writeJSON(t, w, map[string]any{
			"output": []any{map[string]any{
				"type": "message",
				"content": []any{map[string]any{
					"type": "output_text",
					"text": "Tuesday.",
				}},
			}},
		})
	})

	conversation := session.Conversation{
		Summary: "The user is planning a trip.",
		Messages: []session.Message{
			{Speaker: session.SpeakerUser, Text: "My flight is Monday."},
			{Speaker: session.SpeakerAssistant, Text: "I can help plan around it."},
		},
	}
	if _, err := agent.Respond(
		context.Background(),
		tool.Scope{},
		"What about the next day?",
		conversation,
		nil,
	); err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	if len(request.Input) != 4 {
		t.Fatalf("input count = %d, want 4", len(request.Input))
	}
	wantRoles := []string{"user", "user", "assistant", "user"}
	wantText := []string{
		"Earlier conversation summary:\n" + conversation.Summary,
		conversation.Messages[0].Text,
		conversation.Messages[1].Text,
		"What about the next day?",
	}
	for index := range request.Input {
		var message inputMessage
		if err := json.Unmarshal(request.Input[index], &message); err != nil {
			t.Fatalf("decode input %d: %v", index, err)
		}
		if message.Role != wantRoles[index] || message.Content != wantText[index] {
			t.Fatalf("input %d = %#v", index, message)
		}
	}
}

func TestAgentReportsAPIError(t *testing.T) {
	t.Parallel()

	agent := testAgent(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(t, w, map[string]any{
			"error": map[string]string{"message": "invalid request"},
		})
	})

	_, err := agent.Respond(
		context.Background(),
		tool.Scope{},
		"hello",
		session.Conversation{},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "invalid request") {
		t.Fatalf("Respond() error = %v", err)
	}
}

func testAgent(
	t *testing.T,
	handler http.HandlerFunc,
) *Agent {
	t.Helper()

	agent, err := NewAgent("test-key", "test-model", nil)
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}

	httpClient := &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			return recorder.Result(), nil
		}),
	}
	agent.client, err = responses.New("test-key", "test-model", responses.Config{HTTPClient: httpClient})
	if err != nil {
		t.Fatalf("configure test agent: %v", err)
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
