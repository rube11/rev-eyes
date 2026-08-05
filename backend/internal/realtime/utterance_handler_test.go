package realtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type failingJSONWriter struct {
	err error
}

func (writer failingJSONWriter) WriteJSON(any) error {
	return writer.err
}

func TestTranscribeConnectionCancelsStreamWhenMessageDeliveryFails(t *testing.T) {
	streamCanceled := make(chan struct{})
	server := NewServer(transcriberFunc(func(
		ctx context.Context,
		_ <-chan []byte,
		completed chan<- string,
		_ stt.TranscriptObserver,
	) error {
		completed <- "hello"
		<-ctx.Done()
		close(streamCanceled)
		return ctx.Err()
	}), Handlers{
		Utterance: func(
			context.Context,
			tool.Scope,
			string,
		) (UtteranceResult, error) {
			return UtteranceResult{Text: "reply"}, nil
		},
	})
	wantErr := errors.New("socket write failed")
	err := server.transcribeConnection(
		context.Background(),
		tool.Scope{UserID: "user", SessionID: "session"},
		failingJSONWriter{err: wantErr},
		make(chan []byte),
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("transcribeConnection() error = %v, want %v", err, wantErr)
	}
	receive(t, streamCanceled)
}

func TestCompletedUtterancesAreSerializedWithinOneSession(t *testing.T) {
	firstRelease := make(chan struct{})
	entered := make(chan string, 2)
	server := NewServer(echoTranscriber{}, Handlers{
		Utterance: func(
			_ context.Context,
			_ tool.Scope,
			utterance string,
		) (UtteranceResult, error) {
			entered <- utterance
			if utterance == "first" {
				<-firstRelease
			}
			return UtteranceResult{}, nil
		},
	})
	scope := tool.Scope{UserID: "user", SessionID: "session"}
	errors := make(chan error, 2)
	go func() {
		errors <- server.handleCompletedUtterance(
			context.Background(),
			scope,
			discardJSONWriter{},
			"first",
			utteranceDelivery{},
		)
	}()
	if utterance := receive(t, entered); utterance != "first" {
		t.Fatalf("first entered utterance = %q", utterance)
	}
	go func() {
		errors <- server.handleCompletedUtterance(
			context.Background(),
			scope,
			discardJSONWriter{},
			"second",
			utteranceDelivery{},
		)
	}()

	select {
	case utterance := <-entered:
		t.Fatalf("concurrent utterance entered handler: %q", utterance)
	case <-time.After(20 * time.Millisecond):
	}
	close(firstRelease)
	if err := receive(t, errors); err != nil {
		t.Fatalf("first utterance error = %v", err)
	}
	if utterance := receive(t, entered); utterance != "second" {
		t.Fatalf("second entered utterance = %q", utterance)
	}
	if err := receive(t, errors); err != nil {
		t.Fatalf("second utterance error = %v", err)
	}
}
