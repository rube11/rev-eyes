package jev

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewValidatesDependencies(t *testing.T) {
	if _, err := New(" "); !errors.Is(err, ErrAPIKeyRequired) {
		t.Fatalf("New blank key error = %v, want %v", err, ErrAPIKeyRequired)
	}
	if _, err := NewClient("key", nil); !errors.Is(err, ErrHTTPClientRequired) {
		t.Fatalf("NewClient nil HTTP client error = %v, want %v", err, ErrHTTPClientRequired)
	}
}

func TestEvaluate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q, want Bearer secret", got)
		}
		var payload Request
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if payload.Model != defaultModel {
			t.Errorf("model = %q, want %q", payload.Model, defaultModel)
		}
		if len(payload.Questions) != 3 {
			t.Errorf("questions = %d, want 3", len(payload.Questions))
		}

		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{
          "model":"jev-latest",
          "answers":{
            "urgent":{"type":"noul","noul":0.92},
            "route":{"type":"choice","choice":"technical","probabilities":{"billing":0.1,"technical":0.9},"confidence":0.8},
            "frustration":{"type":"score","score":1.5,"legend":{"0":"calm","1":"concerned","2":"angry"},"probabilities":{"0":0.1,"1":0.3,"2":0.6},"confidence":0.7}
          },
          "usage":{"input_tokens":100,"output_tokens":20}
        }`)
	}))
	defer server.Close()

	client, err := NewClient("secret", server.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client.endpoint = server.URL
	response, err := client.Evaluate(context.Background(), Request{
		State: map[string]any{"message": "My account is broken and I need help now."},
		Questions: map[string]Question{
			"urgent": {Type: QuestionNoul, Instructions: "Does `message` convey urgency?"},
			"route": {
				Type: QuestionChoice, Instructions: "Which team should handle `message`?",
				Criteria: map[string]any{"billing": nil, "technical": "Technical problems"},
			},
			"frustration": {
				Type: QuestionScore, Instructions: "How frustrated is the author of `message`?",
				Criteria: []string{"Calm", "Concerned", "Angry"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if response.Answers["urgent"].Noul != 0.92 {
		t.Errorf("urgent = %v, want 0.92", response.Answers["urgent"].Noul)
	}
	if response.Answers["route"].Choice != "technical" {
		t.Errorf("route = %q, want technical", response.Answers["route"].Choice)
	}
	if response.Answers["frustration"].Score != 1.5 {
		t.Errorf("frustration = %v, want 1.5", response.Answers["frustration"].Score)
	}
}

func TestEvaluateReturnsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(writer, `{"detail":"invalid question"}`)
	}))
	defer server.Close()

	client, err := NewClient("secret", server.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client.endpoint = server.URL
	_, err = client.Evaluate(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "invalid question") {
		t.Fatalf("error = %v, want API message", err)
	}
}
