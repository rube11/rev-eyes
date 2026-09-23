package stt

import (
	"context"
	"errors"
	"reflect"
	"testing"

	websocket "github.com/deepgram/deepgram-go-sdk/v3/pkg/api/listen/v1/websocket"
	msginterfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/api/listen/v1/websocket/interfaces"
)

func TestPersistentDeepgramEndpointsKeepAcceptingUtterances(t *testing.T) {
	completed := make(chan Utterance, 2)
	var updates []string
	handler := newDeepgramHandler(context.Background(), completed, func(text string) error {
		updates = append(updates, text)
		return nil
	}, true)
	for _, text := range []string{"glasses hello", "glasses hello"} {
		if err := handler.Message(deepgramMessage(text, true, true, false)); err != nil {
			t.Fatal(err)
		}
		if got := <-completed; got.Text != text {
			t.Fatalf("got %q, want %q", got, text)
		}
		select {
		case <-handler.Endpointed():
			t.Fatal("persistent utterance requested connection closure")
		default:
		}
	}
	if want := []string{"glasses hello", "glasses hello"}; !reflect.DeepEqual(updates, want) {
		t.Fatalf("updates = %#v, want %#v", updates, want)
	}
}

func TestPersistentDeepgramCompletesOnUtteranceEndWhenNoiseBlocksSpeechFinal(t *testing.T) {
	completed := make(chan Utterance, 1)
	handler := newDeepgramHandler(context.Background(), completed, func(string) error { return nil }, true)
	if err := handler.Message(deepgramMessage("hey glasses what is next", true, false, false)); err != nil {
		t.Fatal(err)
	}
	assertNoCompletedUtterance(t, completed)
	if err := handler.UtteranceEnd(&msginterfaces.UtteranceEndResponse{LastWordEnd: 2.4}); err != nil {
		t.Fatal(err)
	}
	select {
	case utterance := <-completed:
		if utterance.Text != "hey glasses what is next" {
			t.Fatalf("completed utterance = %q", utterance)
		}
	default:
		t.Fatal("UtteranceEnd did not complete the paused utterance")
	}
}

func TestDeepgramOptionsEnableBothEndOfTurnSignals(t *testing.T) {
	options := liveTranscriptionOptions()
	if options.Endpointing != speechEndpointSilence {
		t.Fatalf("Endpointing = %q", options.Endpointing)
	}
	if options.UtteranceEndMs != utteranceEndSilence || !options.InterimResults {
		t.Fatalf("UtteranceEnd options = %+v", options)
	}
	if options.VadEvents {
		t.Fatal("unused VAD events should stay disabled")
	}
}

func TestDeepgramIgnoresStaleUtteranceEnd(t *testing.T) {
	completed := make(chan Utterance, 1)
	handler := newDeepgramHandler(context.Background(), completed, func(string) error { return nil }, true)
	if err := handler.Message(deepgramMessage("hey glasses", true, false, false)); err != nil {
		t.Fatal(err)
	}
	if err := handler.UtteranceEnd(&msginterfaces.UtteranceEndResponse{LastWordEnd: -1}); err != nil {
		t.Fatal(err)
	}
	assertNoCompletedUtterance(t, completed)
	if got := handler.Transcript(); got != "hey glasses" {
		t.Fatalf("transcript = %q", got)
	}
}

func TestDeepgramRouterDispatchesRawUtteranceEndToHandler(t *testing.T) {
	completed := make(chan Utterance, 1)
	handler := newDeepgramHandler(context.Background(), completed, func(string) error { return nil }, true)
	if err := handler.Message(deepgramMessage("hey glasses", true, false, false)); err != nil {
		t.Fatal(err)
	}
	router := websocket.NewCallbackRouter(handler)
	if err := router.Message([]byte(`{"type":"UtteranceEnd","channel":[0,1],"last_word_end":2.4}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case utterance := <-completed:
		if utterance.Text != "hey glasses" {
			t.Fatalf("completed utterance = %q", utterance)
		}
	default:
		t.Fatal("raw UtteranceEnd event was not dispatched")
	}
}

func TestDeepgramHandlerCompletesAtSpeechEndpoint(t *testing.T) {
	completed := make(chan Utterance, 1)
	var updates []string
	handler := newDeepgramHandler(
		context.Background(),
		completed,
		func(text string) error {
			updates = append(updates, text)
			return nil
		}, false)

	messages := []struct {
		message        *msginterfaces.MessageResponse
		wantTranscript string
	}{
		{deepgramMessage("remind me", false, false, false), ""},
		{deepgramMessage("remind me", false, false, false), ""},
		{deepgramMessage("remind me", true, false, false), "remind me"},
		{deepgramMessage("tomorrow", false, false, false), "remind me"},
		{deepgramMessage("tomorrow", false, false, false), "remind me"},
		{deepgramMessage("tomorrow", true, false, false), "remind me tomorrow"},
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
	if err := handler.Message(deepgramMessage("at nine", true, true, false)); err != nil {
		t.Fatalf("speech endpoint Message() error = %v", err)
	}

	wantUpdates := []string{
		"remind me",
		"remind me tomorrow",
		"remind me tomorrow at nine",
	}
	if !reflect.DeepEqual(updates, wantUpdates) {
		t.Fatalf("updates = %#v, want %#v", updates, wantUpdates)
	}

	select {
	case utterance := <-completed:
		if utterance.Text != "remind me tomorrow at nine" {
			t.Fatalf("completed utterance = %q", utterance)
		}
	default:
		t.Fatal("speech endpoint did not complete the utterance")
	}
	if got := handler.Transcript(); got != "" {
		t.Fatalf("transcript after speech endpoint = %q, want empty", got)
	}
	select {
	case <-handler.Endpointed():
	default:
		t.Fatal("speech endpoint signal was not closed")
	}
}

func TestDeepgramHandlerIncludesFinalSegmentFromExplicitFinalize(t *testing.T) {
	completed := make(chan Utterance, 1)
	var updates []string
	handler := newDeepgramHandler(
		context.Background(),
		completed,
		func(text string) error {
			updates = append(updates, text)
			return nil
		}, false)

	if err := handler.Message(deepgramMessage("hello", true, false, false)); err != nil {
		t.Fatalf("final segment Message() error = %v", err)
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
		if utterance.Text != "hello world" {
			t.Fatalf("completed utterance = %q, want %q", utterance, "hello world")
		}
	default:
		t.Fatal("explicitly finalized utterance was not completed")
	}
}

func TestDeepgramHandlerEmptyFinalizeSignalsWithoutCompleting(t *testing.T) {
	completed := make(chan Utterance, 1)
	handler := newDeepgramHandler(
		context.Background(),
		completed,
		func(string) error { return nil }, false)

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
		make(chan Utterance, 1),
		func(string) error {
			return wantErr
		}, false)

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

func assertNoCompletedUtterance(t *testing.T, completed <-chan Utterance) {
	t.Helper()

	select {
	case utterance := <-completed:
		t.Fatalf("unexpected completed utterance %q", utterance)
	default:
	}
}
