package realtime

import (
	"context"
	"fmt"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type repeatMessageWriter struct {
	messages []serverMessage
}

func (writer *repeatMessageWriter) WriteJSON(value any) error {
	message, ok := value.(serverMessage)
	if !ok {
		return fmt.Errorf("unexpected message type %T", value)
	}
	writer.messages = append(writer.messages, message)
	return nil
}

func TestRepeatRequestBypassesUtteranceHandler(t *testing.T) {
	t.Parallel()

	handlerCalled := false
	server := NewServer(echoTranscriber{}, Handlers{
		Utterance: func(
			context.Context,
			tool.Scope,
			string,
		) (UtteranceResult, error) {
			handlerCalled = true
			return UtteranceResult{Text: "should not be sent"}, nil
		},
	})
	writer := &repeatMessageWriter{}
	err := server.handleCompletedUtterance(
		context.Background(),
		tool.Scope{},
		writer,
		"Show that again.",
		utteranceDelivery{messageID: "candidate-repeat", announceThinking: true},
	)
	if err != nil {
		t.Fatalf("handleCompletedUtterance() error = %v", err)
	}

	if len(writer.messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(writer.messages))
	}
	message := writer.messages[0]
	if message.Type != assistantRepeatMessageType || message.ID != "candidate-repeat" {
		t.Fatalf("repeat message = %+v", message)
	}
	if handlerCalled {
		t.Fatal("repeat request reached the utterance handler")
	}
}

func TestRepeatRequestVocabularyIsNarrow(t *testing.T) {
	t.Parallel()

	for _, utterance := range []string{
		"Show that again.",
		"show it again",
		"Repeat that, please!",
	} {
		if !isRepeatRequest(utterance) {
			t.Errorf("isRepeatRequest(%q) = false", utterance)
		}
	}
	for _, utterance := range []string{
		"show that to me again",
		"repeat the reminder",
		"what did you say",
	} {
		if isRepeatRequest(utterance) {
			t.Errorf("isRepeatRequest(%q) = true", utterance)
		}
	}
}
