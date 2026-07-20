package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/assistant"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type fakeUtteranceService struct {
	handle func(context.Context, tool.Scope, string) (assistant.Outcome, error)
}

func (f fakeUtteranceService) HandleUtterance(
	ctx context.Context,
	scope tool.Scope,
	utterance string,
) (assistant.Outcome, error) {
	return f.handle(ctx, scope, utterance)
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

type fakeMemoryStore struct {
	remember func(context.Context, tool.Scope, string, string) error
}

func (f fakeMemoryStore) Remember(
	ctx context.Context,
	scope tool.Scope,
	sourceUtteranceID string,
	text string,
) error {
	return f.remember(ctx, scope, sourceUtteranceID, text)
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
			utterance string,
		) (assistant.Outcome, error) {
			calls = append(calls, "handle:"+utterance)
			return assistant.Outcome{Response: "Here you go."}, nil
		},
	}
	memories := fakeMemoryStore{
		remember: func(context.Context, tool.Scope, string, string) error {
			t.Fatal("Remember() was called")
			return nil
		},
	}

	response, err := handleUtterance(
		context.Background(),
		tool.Scope{UserID: "user-123", SessionID: "session-123"},
		"Where am I?",
		service,
		transcripts,
		memories,
	)
	if err != nil {
		t.Fatalf("handleUtterance() error = %v", err)
	}
	if response != "Here you go." {
		t.Fatalf("response = %q", response)
	}

	want := []string{
		"user:Where am I?",
		"handle:Where am I?",
		"assistant:Here you go.",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestHandleUtterancePersistsExplicitMemory(t *testing.T) {
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
			_ context.Context,
			_ tool.Scope,
			utterance string,
		) (assistant.Outcome, error) {
			calls = append(calls, "handle:"+utterance)
			return assistant.Outcome{
				Decision: assistant.Decision{
					Action: assistant.ActionRemember,
					Query:  "The user's boss is Maya.",
				},
			}, nil
		},
	}
	memories := fakeMemoryStore{
		remember: func(
			_ context.Context,
			gotScope tool.Scope,
			sourceUtteranceID string,
			text string,
		) error {
			if gotScope != scope {
				t.Fatalf("scope = %+v, want %+v", gotScope, scope)
			}
			calls = append(
				calls,
				"remember:"+sourceUtteranceID+":"+text,
			)
			return nil
		},
	}

	response, err := handleUtterance(
		context.Background(),
		scope,
		"Remember that my boss is Maya.",
		service,
		transcripts,
		memories,
	)
	if err != nil {
		t.Fatalf("handleUtterance() error = %v", err)
	}
	if response != memoryAcknowledgment {
		t.Fatalf("response = %q", response)
	}

	want := []string{
		"user:Remember that my boss is Maya.",
		"handle:Remember that my boss is Maya.",
		"remember:utterance-123:The user's boss is Maya.",
		"assistant:" + memoryAcknowledgment,
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
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
		) (assistant.Outcome, error) {
			t.Fatal("HandleUtterance() was called")
			return assistant.Outcome{}, nil
		},
	}
	memories := fakeMemoryStore{
		remember: func(context.Context, tool.Scope, string, string) error {
			t.Fatal("Remember() was called")
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
