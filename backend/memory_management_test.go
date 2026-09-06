package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/assistant"
	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/realtime"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func TestHandleUtteranceCorrectsMemorySynchronously(t *testing.T) {
	wantScope := tool.Scope{UserID: "user-123", SessionID: "session-123"}
	transcripts := fakeTranscriptStore{append: func(
		context.Context,
		tool.Scope,
		session.Speaker,
		string,
	) (string, error) {
		return "utterance-123", nil
	}}
	service := fakeUtteranceService{handle: func(
		context.Context,
		tool.Scope,
		string,
		string,
	) (assistant.Outcome, error) {
		return assistant.Outcome{Decision: assistant.Decision{
			Action: assistant.ActionMemoryCorrect,
			Query:  "My protein target is 150 grams.",
		}}, nil
	}}
	memories := fakeMemoryService{
		capture: func(tool.Scope, string, string) bool {
			t.Fatal("Capture() was called for a memory correction")
			return false
		},
		rememberExplicit: func(
			_ context.Context,
			scope tool.Scope,
			sourceID string,
			text string,
		) error {
			if scope != wantScope ||
				sourceID != "utterance-123" ||
				text != "Change my protein target to 150 grams." {
				t.Fatalf("RememberExplicit(%#v, %q, %q)", scope, sourceID, text)
			}
			return nil
		},
	}

	result, err := handleUtterance(
		context.Background(),
		wantScope,
		"Change my protein target to 150 grams.",
		service,
		transcripts,
		memories,
	)
	if err != nil {
		t.Fatalf("handleUtterance() error = %v", err)
	}
	if result.Text != memoryCorrectionAcknowledgment {
		t.Fatalf("result = %#v", result)
	}
	if !reflect.DeepEqual(
		result.WorkspaceResources,
		[]realtime.WorkspaceResource{
			realtime.WorkspaceConversations,
			realtime.WorkspaceMemories,
		},
	) {
		t.Fatalf("workspace resources = %#v", result.WorkspaceResources)
	}
}

func TestMemoryCorrectionDoesNotAcknowledgeFailedOrIncompleteWrites(t *testing.T) {
	writeFailure := errors.New("memory store unavailable")
	for _, test := range []struct {
		name      string
		response  string
		writeErr  error
		wantText  string
		wantWrite bool
	}{
		{"unsafe", "", memory.ErrUnsafeMemory, unsafeMemoryAcknowledgment, true},
		{"no facts", "", memory.ErrNoMemoryCandidates, noMemoryAcknowledgment, true},
		{"failed save", "", writeFailure, "", true},
		{"needs clarification", "Which fact should I correct?", nil, "Which fact should I correct?", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var saved bool
			var replies []string
			service := fakeUtteranceService{handle: func(context.Context, tool.Scope, string, string) (assistant.Outcome, error) {
				return assistant.Outcome{
					Decision: assistant.Decision{Action: assistant.ActionMemoryCorrect},
					Response: test.response,
				}, nil
			}}
			transcripts := fakeTranscriptStore{append: func(_ context.Context, _ tool.Scope, speaker session.Speaker, text string) (string, error) {
				if speaker == session.SpeakerAssistant {
					replies = append(replies, text)
				}
				return "utterance", nil
			}}
			memories := fakeMemoryService{
				capture: func(tool.Scope, string, string) bool {
					t.Fatal("correction was queued as a new background memory")
					return false
				},
				rememberExplicit: func(context.Context, tool.Scope, string, string) error {
					saved = true
					return test.writeErr
				},
			}
			result, err := handleUtterance(context.Background(), tool.Scope{UserID: "user", SessionID: "session"},
				"Change my protein target", service, transcripts, memories)
			if test.writeErr == writeFailure {
				if !errors.Is(err, writeFailure) || len(replies) != 0 {
					t.Fatalf("failed write: error = %v, replies = %v", err, replies)
				}
			} else if err != nil || !reflect.DeepEqual(replies, []string{test.wantText}) {
				t.Fatalf("error = %v, replies = %v", err, replies)
			}
			if saved != test.wantWrite || result.Text != test.wantText {
				t.Fatalf("saved = %v, result = %#v", saved, result)
			}
			if !reflect.DeepEqual(result.WorkspaceResources, []realtime.WorkspaceResource{realtime.WorkspaceConversations}) {
				t.Fatalf("failed or incomplete correction refreshed memories: %v", result.WorkspaceResources)
			}
		})
	}
}

func TestMemoryManagementCommandsAreNotLearnedAsMemories(t *testing.T) {
	for _, action := range []assistant.Action{
		assistant.ActionRemember,
		assistant.ActionMemoryReview,
		assistant.ActionMemoryCorrect,
		assistant.ActionMemoryForget,
	} {
		if shouldCaptureMemory(action) {
			t.Errorf("shouldCaptureMemory(%q) = true", action)
		}
	}
	for _, action := range []assistant.Action{
		assistant.ActionRespond,
		assistant.ActionStateUpdate,
		assistant.ActionStateTransition,
	} {
		if !shouldCaptureMemory(action) {
			t.Errorf("shouldCaptureMemory(%q) = false", action)
		}
	}
}

func TestHandleUtteranceMarksForgottenMemoryWorkspaceChanged(t *testing.T) {
	transcripts := fakeTranscriptStore{append: func(
		context.Context,
		tool.Scope,
		session.Speaker,
		string,
	) (string, error) {
		return "utterance-123", nil
	}}
	service := fakeUtteranceService{handle: func(
		context.Context,
		tool.Scope,
		string,
		string,
	) (assistant.Outcome, error) {
		return assistant.Outcome{
			Decision:      assistant.Decision{Action: assistant.ActionMemoryForget},
			Response:      "Okay, I forgot that.",
			MemoryChanged: true,
		}, nil
	}}
	memories := fakeMemoryService{capture: func(tool.Scope, string, string) bool {
		t.Fatal("Capture() was called for a forget command")
		return false
	}}

	result, err := handleUtterance(
		context.Background(),
		tool.Scope{UserID: "user-123", SessionID: "session-123"},
		"Forget that.",
		service,
		transcripts,
		memories,
	)
	if err != nil {
		t.Fatalf("handleUtterance() error = %v", err)
	}
	if result.Text != "Okay, I forgot that." ||
		!reflect.DeepEqual(
			result.WorkspaceResources,
			[]realtime.WorkspaceResource{
				realtime.WorkspaceConversations,
				realtime.WorkspaceMemories,
			},
		) {
		t.Fatalf("result = %#v", result)
	}
}
