package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func TestMemoryReviewDisablesToolsAndUsesConversationalInstructions(t *testing.T) {
	for _, toolCall := range []bool{false, true} {
		t.Run(map[bool]string{false: "answer", true: "unexpected tool blocked"}[toolCall], func(t *testing.T) {
			registered := &recordingTool{name: "propose_task", mutating: true}
			agent := testAgent(t, registered, func(w http.ResponseWriter, r *http.Request) {
				var request createRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				if len(request.Tools) != 0 {
					t.Fatal("review exposed tools")
				}
				for _, want := range []string{"You are Eyes", "read-only memory question", "not that no memories exist", "one practical next step", "do not end every reply with a question", "Keep ownership of facts explicit", "Do not invent deadlines"} {
					if !strings.Contains(request.Instructions, want) {
						t.Errorf("instructions missing %q", want)
					}
				}
				if toolCall {
					writeJSON(t, w, map[string]any{"output": []any{map[string]any{"type": "function_call", "name": "propose_task", "call_id": "bad", "arguments": "{}"}}})
				} else {
					writeJSON(t, w, map[string]any{"output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "You're studying computer science at UNLV."}}}}})
				}
			})
			_, err := agent.Respond(context.Background(), tool.Scope{MemoryReview: true}, "What school do I go to?", session.Conversation{}, []memory.Card{{Title: "University", Summary: "The user attends UNLV."}})
			if toolCall && (err == nil || !strings.Contains(err.Error(), "cannot execute tools")) {
				t.Fatalf("unexpected tool wasn't rejected: %v", err)
			}
			if !toolCall && err != nil {
				t.Fatal(err)
			}
			if len(registered.scopes) != 0 {
				t.Fatal("review executed a registered tool")
			}
		})
	}
}

func TestRouterPromptHandlesMemoryRepairsAndEquivalentTerms(t *testing.T) {
	for _, want := range []string{"latest_utterance", "memory_review_all=true", "I meant me", "school", "university", "Never infer a replacement fact", "ambiguous multi-fact profile"} {
		if !strings.Contains(routerPrompt, want) {
			t.Errorf("missing %q", want)
		}
	}
}
