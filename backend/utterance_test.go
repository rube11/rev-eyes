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

type fakeUtteranceService struct {
	handle func(context.Context, tool.Scope, string, string) (assistant.Outcome, error)
}

func (f fakeUtteranceService) HandleUtterance(
	ctx context.Context,
	scope tool.Scope,
	utteranceID string,
	utterance string,
) (assistant.Outcome, error) {
	return f.handle(ctx, scope, utteranceID, utterance)
}

type fakeTranscriptStore struct {
	append func(context.Context, tool.Scope, session.Speaker, string) (string, error)
}

func (f fakeTranscriptStore) Append(
	ctx context.Context,
	scope tool.Scope,
	speaker session.Speaker,
	text string,
) (string, error) {
	return f.append(ctx, scope, speaker, text)
}

type fakeMemoryService struct {
	capture          func(tool.Scope, string, string) bool
	rememberExplicit func(context.Context, tool.Scope, string, string) error
}

func (f fakeMemoryService) Capture(scope tool.Scope, sourceID, text string) bool {
	if f.capture == nil {
		return true
	}
	return f.capture(scope, sourceID, text)
}

func (f fakeMemoryService) RememberExplicit(
	ctx context.Context,
	scope tool.Scope,
	sourceID string,
	text string,
) error {
	if f.rememberExplicit == nil {
		return nil
	}
	return f.rememberExplicit(ctx, scope, sourceID, text)
}

func TestHandleUtteranceUsesAtomicPipelineForExplicitMemory(t *testing.T) {
	wantScope := tool.Scope{UserID: "user-123", SessionID: "session-123"}
	transcripts := fakeTranscriptStore{
		append: func(
			context.Context,
			tool.Scope,
			session.Speaker,
			string,
		) (string, error) {
			return "utterance-123", nil
		},
	}
	service := fakeUtteranceService{
		handle: func(
			context.Context,
			tool.Scope,
			string,
			string,
		) (assistant.Outcome, error) {
			return assistant.Outcome{Decision: assistant.Decision{
				Action: assistant.ActionRemember,
				Query:  "The user's protein target is 150 grams.",
			}}, nil
		},
	}
	memories := fakeMemoryService{
		capture: func(tool.Scope, string, string) bool {
			t.Fatal("Capture() was called for an explicit remember action")
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
				text != "Remember that my protein target is 150 grams." {
				t.Fatalf("RememberExplicit(%#v, %q, %q)", scope, sourceID, text)
			}
			return nil
		},
	}

	result, err := handleUtterance(
		context.Background(),
		wantScope,
		"Remember that my protein target is 150 grams.",
		service,
		transcripts,
		memories,
	)
	if err != nil {
		t.Fatalf("handleUtterance() error = %v", err)
	}
	if result.Text != memoryAcknowledgment {
		t.Fatalf("response = %#v", result)
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

func TestHandleUtteranceRefusesToStoreCredentials(t *testing.T) {
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
			Action: assistant.ActionRemember,
		}}, nil
	}}
	memories := fakeMemoryService{rememberExplicit: func(
		context.Context,
		tool.Scope,
		string,
		string,
	) error {
		return memory.ErrUnsafeMemory
	}}
	result, err := handleUtterance(
		context.Background(),
		tool.Scope{UserID: "user-123", SessionID: "session-123"},
		"Remember that my password is secret.",
		service,
		transcripts,
		memories,
	)
	if err != nil {
		t.Fatalf("handleUtterance() error = %v", err)
	}
	if result.Text != unsafeMemoryAcknowledgment {
		t.Fatalf("response = %#v", result)
	}
	if !reflect.DeepEqual(
		result.WorkspaceResources,
		[]realtime.WorkspaceResource{realtime.WorkspaceConversations},
	) {
		t.Fatalf("workspace resources = %#v", result.WorkspaceResources)
	}
}

func TestHandleUtteranceDoesNotAcknowledgeAnEmptyExplicitMemory(t *testing.T) {
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
			Action: assistant.ActionRemember,
		}}, nil
	}}
	memories := fakeMemoryService{rememberExplicit: func(
		context.Context,
		tool.Scope,
		string,
		string,
	) error {
		return memory.ErrNoMemoryCandidates
	}}

	result, err := handleUtterance(
		context.Background(),
		tool.Scope{UserID: "user-123", SessionID: "session-123"},
		"Remember something.",
		service,
		transcripts,
		memories,
	)
	if err != nil {
		t.Fatalf("handleUtterance() error = %v", err)
	}
	if result.Text != noMemoryAcknowledgment {
		t.Fatalf("response = %#v", result)
	}
	if !reflect.DeepEqual(
		result.WorkspaceResources,
		[]realtime.WorkspaceResource{realtime.WorkspaceConversations},
	) {
		t.Fatalf("workspace resources = %#v", result.WorkspaceResources)
	}
}

func TestHandleUtteranceQueuesSilentStateForBackgroundLearning(t *testing.T) {
	var calls []string
	scope := tool.Scope{UserID: "user-123", SessionID: "session-123"}
	transcripts := fakeTranscriptStore{
		append: func(
			_ context.Context,
			_ tool.Scope,
			speaker session.Speaker,
			text string,
		) (string, error) {
			calls = append(calls, string(speaker)+":"+text)
			return "utterance-123", nil
		},
	}
	service := fakeUtteranceService{
		handle: func(
			context.Context,
			tool.Scope,
			string,
			string,
		) (assistant.Outcome, error) {
			calls = append(calls, "route")
			return assistant.Outcome{
				Decision: assistant.Decision{Action: assistant.ActionStateUpdate},
			}, nil
		},
	}
	memories := fakeMemoryService{
		capture: func(gotScope tool.Scope, sourceID, text string) bool {
			if gotScope != scope || sourceID != "utterance-123" || text != "I am working out." {
				t.Fatalf("Capture(%+v, %q, %q)", gotScope, sourceID, text)
			}
			calls = append(calls, "capture")
			return true
		},
	}

	result, err := handleUtterance(
		context.Background(),
		scope,
		"I am working out.",
		service,
		transcripts,
		memories,
	)
	if err != nil {
		t.Fatalf("handleUtterance() error = %v", err)
	}
	if result.Text != "" || result.AwaitingConfirmation {
		t.Fatalf("result = %+v, want silent state update", result)
	}
	want := []string{"user:I am working out.", "route", "capture"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestHandleUtterancePersistsFinalizedTranscriptInOrder(t *testing.T) {
	var calls []string
	transcripts := fakeTranscriptStore{
		append: func(
			_ context.Context,
			_ tool.Scope,
			speaker session.Speaker,
			text string,
		) (string, error) {
			calls = append(calls, string(speaker)+":"+text)
			return "utterance-123", nil
		},
	}
	service := fakeUtteranceService{
		handle: func(
			_ context.Context,
			_ tool.Scope,
			utteranceID string,
			utterance string,
		) (assistant.Outcome, error) {
			calls = append(calls, "handle:"+utteranceID+":"+utterance)
			return assistant.Outcome{Response: "Here you go."}, nil
		},
	}
	response, err := handleUtterance(
		context.Background(),
		tool.Scope{UserID: "user-123", SessionID: "session-123"},
		"Where am I?",
		service,
		transcripts,
		fakeMemoryService{},
	)
	if err != nil {
		t.Fatalf("handleUtterance() error = %v", err)
	}
	if response.Text != "Here you go." || response.AwaitingConfirmation {
		t.Fatalf("response = %+v", response)
	}
	if !reflect.DeepEqual(
		response.WorkspaceResources,
		[]realtime.WorkspaceResource{realtime.WorkspaceConversations},
	) {
		t.Fatalf("workspace resources = %#v", response.WorkspaceResources)
	}

	want := []string{
		"user:Where am I?",
		"handle:utterance-123:Where am I?",
		"assistant:Here you go.",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestHandleUtteranceMarksProposalsAwaitingConfirmation(t *testing.T) {
	for _, action := range []assistant.Action{
		assistant.ActionProposeTask,
		assistant.ActionProposeWatch,
	} {
		t.Run(string(action), func(t *testing.T) {
			transcripts := fakeTranscriptStore{
				append: func(
					context.Context,
					tool.Scope,
					session.Speaker,
					string,
				) (string, error) {
					return "utterance-123", nil
				},
			}
			service := fakeUtteranceService{
				handle: func(
					context.Context,
					tool.Scope,
					string,
					string,
				) (assistant.Outcome, error) {
					return assistant.Outcome{
						Decision:        assistant.Decision{Action: action},
						Response:        "Should I do that?",
						ProposalCreated: true,
					}, nil
				},
			}
			response, err := handleUtterance(
				context.Background(),
				tool.Scope{UserID: "user-123", SessionID: "session-123"},
				"Please do something later.",
				service,
				transcripts,
				fakeMemoryService{},
			)
			if err != nil {
				t.Fatalf("handleUtterance() error = %v", err)
			}
			if response.Text != "Should I do that?" || !response.AwaitingConfirmation {
				t.Fatalf("response = %+v", response)
			}
			wantResource := realtime.WorkspaceTasks
			if action == assistant.ActionProposeWatch {
				wantResource = realtime.WorkspaceWatches
			}
			if !reflect.DeepEqual(
				response.WorkspaceResources,
				[]realtime.WorkspaceResource{
					realtime.WorkspaceConversations,
					wantResource,
				},
			) {
				t.Fatalf("workspace resources = %#v", response.WorkspaceResources)
			}
		})
	}
}

func TestHandleUtteranceDoesNotPredictConfirmationFromRouterAction(t *testing.T) {
	transcripts := fakeTranscriptStore{
		append: func(
			context.Context,
			tool.Scope,
			session.Speaker,
			string,
		) (string, error) {
			return "utterance-123", nil
		},
	}
	service := fakeUtteranceService{
		handle: func(
			context.Context,
			tool.Scope,
			string,
			string,
		) (assistant.Outcome, error) {
			return assistant.Outcome{
				Decision: assistant.Decision{Action: assistant.ActionProposeTask},
				Response: "What time does class end?",
			}, nil
		},
	}

	response, err := handleUtterance(
		context.Background(),
		tool.Scope{UserID: "user-123", SessionID: "session-123"},
		"I need to go tomorrow after class.",
		service,
		transcripts,
		fakeMemoryService{},
	)
	if err != nil {
		t.Fatalf("handleUtterance() error = %v", err)
	}
	if response.AwaitingConfirmation {
		t.Fatalf("response = %+v, want no pending confirmation", response)
	}
	if !reflect.DeepEqual(
		response.WorkspaceResources,
		[]realtime.WorkspaceResource{realtime.WorkspaceConversations},
	) {
		t.Fatalf("workspace resources = %#v", response.WorkspaceResources)
	}
}

func TestHandleUtteranceStopsWhenUserTranscriptCannotBePersisted(t *testing.T) {
	persistErr := errors.New("database unavailable")
	transcripts := fakeTranscriptStore{
		append: func(
			context.Context,
			tool.Scope,
			session.Speaker,
			string,
		) (string, error) {
			return "", persistErr
		},
	}
	service := fakeUtteranceService{
		handle: func(
			context.Context,
			tool.Scope,
			string,
			string,
		) (assistant.Outcome, error) {
			t.Fatal("HandleUtterance() was called")
			return assistant.Outcome{}, nil
		},
	}
	memories := fakeMemoryService{
		capture: func(tool.Scope, string, string) bool {
			t.Fatal("Capture() was called")
			return false
		},
		rememberExplicit: func(context.Context, tool.Scope, string, string) error {
			t.Fatal("RememberExplicit() was called")
			return nil
		},
	}

	_, err := handleUtterance(
		context.Background(),
		tool.Scope{UserID: "user-123", SessionID: "session-123"},
		"hello",
		service,
		transcripts,
		memories,
	)
	if !errors.Is(err, persistErr) {
		t.Fatalf("handleUtterance() error = %v, want wrapped persistence error", err)
	}
}
