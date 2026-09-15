package main

import (
	"context"
	"github.com/rube11/rev-eyes/backend/internal/assistant"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
	"testing"
)

func TestTypedEmptyResponseGetsSavedFallbackAndStillLearns(t *testing.T) {
	for _, always := range []bool{false, true} {
		captured := false
		assistantSaved := false
		result, err := handleUtterance(context.Background(), tool.Scope{AlwaysRespond: always}, "hey",
			fakeUtteranceService{handle: func(context.Context, tool.Scope, string, string) (assistant.Outcome, error) {
				return assistant.Outcome{Decision: assistant.Decision{Action: assistant.ActionRespond}}, nil
			}},
			fakeTranscriptStore{append: func(_ context.Context, _ tool.Scope, speaker session.Speaker, text string) (string, error) {
				if speaker == session.SpeakerAssistant {
					assistantSaved = true
					if text == "" {
						t.Fatal("empty assistant transcript")
					}
				}
				return "turn", nil
			}},
			fakeMemoryService{capture: func(tool.Scope, string, string) bool { captured = true; return true }})
		if err != nil {
			t.Fatal(err)
		}
		if !captured {
			t.Fatal("memory learning was bypassed")
		}
		if assistantSaved != always || (result.Text != "") != always {
			t.Fatal("text fallback or audio behavior incorrect")
		}
	}
}
