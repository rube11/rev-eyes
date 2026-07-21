package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/session"
)

func TestAgentCompactsConversationWithoutTools(t *testing.T) {
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
					"text": "  The user is planning a Monday flight.  ",
				}},
			}},
		})
	})

	conversation := session.Conversation{
		Summary: "The user is planning a trip.",
		Messages: []session.Message{{
			Speaker: session.SpeakerUser,
			Text:    "My flight is Monday.",
		}},
	}
	summary, err := agent.Compact(context.Background(), conversation)
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if summary != "The user is planning a Monday flight." {
		t.Fatalf("Compact() = %q", summary)
	}
	if request.Instructions != compactionInstructions ||
		len(request.Tools) != 0 ||
		request.ParallelToolCalls ||
		request.MaxOutputTokens != maxCompactionOutputTokens ||
		len(request.Include) != 0 {
		t.Fatalf("compaction request = %#v", request)
	}
}
