package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

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

func TestAgentIncludesRelevantMemoriesAsUserData(t *testing.T) {
	t.Parallel()

	var request createRequest
	agent := testAgent(t, nil, func(w http.ResponseWriter, r *http.Request) {
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
	agent := testAgent(t, nil, func(w http.ResponseWriter, r *http.Request) {
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

	agent := testAgent(t, nil, func(w http.ResponseWriter, _ *http.Request) {
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
