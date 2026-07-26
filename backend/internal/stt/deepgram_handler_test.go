package stt

import (
	"context"
	"errors"
	"reflect"
	"testing"

	msginterfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/api/listen/v1/websocket/interfaces"
)

func TestDeepgramHandlerAccumulatesNaturalPausesUntilExplicitFinalize(t *testing.T) {
	completed := make(chan string, 1)
	var updates []string
	handler := newDeepgramHandler(
		context.Background(),
		completed,
		func(text string) error {
			updates = append(updates, text)
			return nil
		},
	)

	messages := []struct {
		message        *msginterfaces.MessageResponse
		wantTranscript string
	}{
		{deepgramMessage("remind me", false, false, false), ""},
		{deepgramMessage("remind me", false, false, false), ""},
		{deepgramMessage("remind me", true, true, false), "remind me"},
		{deepgramMessage("tomorrow", false, false, false), "remind me"},
		{deepgramMessage("tomorrow", false, false, false), "remind me"},
		{deepgramMessage("tomorrow", true, true, false), "remind me tomorrow"},
		{deepgramMessage("at nine", true, true, false), "remind me tomorrow at nine"},
	}
	for index, step := range messages {
		if err := handler.Message(step.message); err != nil {
			t.Fatalf("Message() error = %v", err)
		}
		if got := handler.Transcript(); got != step.wantTranscript {
			t.Fatalf("step %d transcript = %q, want %q", index, got, step.wantTranscript)
		}
		assertNoCompletedUtterance(t, completed)
	}

	wantUpdates := []string{
		"remind me",
		"remind me tomorrow",
		"remind me tomorrow at nine",
	}
	if !reflect.DeepEqual(updates, wantUpdates) {
		t.Fatalf("updates = %#v, want %#v", updates, wantUpdates)
	}

	if err := handler.Message(deepgramMessage("", false, false, true)); err != nil {
		t.Fatalf("finalize Message() error = %v", err)
	}
	select {
	case utterance := <-completed:
		if utterance != "remind me tomorrow at nine" {
			t.Fatalf("completed utterance = %q", utterance)
		}
	default:
		t.Fatal("explicitly finalized utterance was not completed")
	}
	if got := handler.Transcript(); got != "" {
		t.Fatalf("transcript after finalize = %q, want empty", got)
	}
	select {
	case <-handler.Finalized():
	default:
		t.Fatal("finalization signal was not closed")
	}
}

func TestDeepgramHandlerIncludesFinalSegmentFromExplicitFinalize(t *testing.T) {
	completed := make(chan string, 1)
	var updates []string
	handler := newDeepgramHandler(
		context.Background(),
		completed,
		func(text string) error {
			updates = append(updates, text)
			return nil
		},
	)

	if err := handler.Message(deepgramMessage("hello", true, true, false)); err != nil {
		t.Fatalf("natural pause Message() error = %v", err)
	}
	assertNoCompletedUtterance(t, completed)

	if err := handler.Message(deepgramMessage("world", true, false, true)); err != nil {
		t.Fatalf("finalize Message() error = %v", err)
	}

	wantUpdates := []string{"hello", "hello world"}
	if !reflect.DeepEqual(updates, wantUpdates) {
		t.Fatalf("updates = %#v, want %#v", updates, wantUpdates)
	}
	select {
	case utterance := <-completed:
		if utterance != "hello world" {
			t.Fatalf("completed utterance = %q, want %q", utterance, "hello world")
		}
	default:
		t.Fatal("explicitly finalized utterance was not completed")
	}
}

func TestDeepgramHandlerEmptyFinalizeSignalsWithoutCompleting(t *testing.T) {
	completed := make(chan string, 1)
	handler := newDeepgramHandler(
		context.Background(),
		completed,
		func(string) error { return nil },
	)

	if err := handler.Message(deepgramMessage("", false, false, true)); err != nil {
		t.Fatalf("Message() error = %v", err)
	}
	assertNoCompletedUtterance(t, completed)
	select {
	case <-handler.Finalized():
	default:
		t.Fatal("finalization signal was not closed")
	}
}

func TestDeepgramHandlerReturnsTranscriptObserverError(t *testing.T) {
	wantErr := errors.New("display unavailable")
	handler := newDeepgramHandler(
		context.Background(),
		make(chan string, 1),
		func(string) error {
			return wantErr
		},
	)

	err := handler.Message(&msginterfaces.MessageResponse{
		Channel: msginterfaces.Channel{
			Alternatives: []msginterfaces.Alternative{{Transcript: "hello"}},
		},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Message() error = %v, want %v", err, wantErr)
	}
}

func deepgramMessage(
	transcript string,
	isFinal bool,
	speechFinal bool,
	fromFinalize bool,
) *msginterfaces.MessageResponse {
	message := &msginterfaces.MessageResponse{
		IsFinal:      isFinal,
		SpeechFinal:  speechFinal,
		FromFinalize: fromFinalize,
	}
	if transcript != "" {
		message.Channel.Alternatives = []msginterfaces.Alternative{{Transcript: transcript}}
	}
	return message
}

func assertNoCompletedUtterance(t *testing.T, completed <-chan string) {
	t.Helper()

	select {
	case utterance := <-completed:
		t.Fatalf("unexpected completed utterance %q", utterance)
	default:
	}
}
