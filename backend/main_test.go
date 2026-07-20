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
	append func(context.Context, tool.Scope, session.Speaker, string) error
}

func (f fakeTranscriptStore) Append(
	ctx context.Context,
	scope tool.Scope,
	speaker session.Speaker,
	text string,
) error {
	return f.append(ctx, scope, speaker, text)
}

func TestHandleUtterancePersistsFinalizedTranscriptInOrder(t *testing.T) {
	var calls []string
	transcripts := fakeTranscriptStore{
		append: func(
			_ context.Context,
			_ tool.Scope,
			speaker session.Speaker,
			text string,
		) error {
			calls = append(calls, string(speaker)+":"+text)
			return nil
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

	response, err := handleUtterance(
		context.Background(),
		tool.Scope{UserID: "user-123", SessionID: "session-123"},
		"Where am I?",
		service,
		transcripts,
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

func TestHandleUtteranceStopsWhenUserTranscriptCannotBePersisted(t *testing.T) {
	persistErr := errors.New("database unavailable")
	transcripts := fakeTranscriptStore{
		append: func(
			context.Context,
			tool.Scope,
			session.Speaker,
			string,
		) error {
			return persistErr
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

	_, err := handleUtterance(
		context.Background(),
		tool.Scope{UserID: "user-123", SessionID: "session-123"},
		"hello",
		service,
		transcripts,
	)
	if !errors.Is(err, persistErr) {
		t.Fatalf("handleUtterance() error = %v, want wrapped persistence error", err)
	}
}
