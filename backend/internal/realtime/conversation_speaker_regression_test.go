package realtime

import (
	"context"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/speech"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

// Exercise the actual automatic-conversation gate after a completed transcript,
// rather than only the speech helper or an already-authorized follow-up.
func TestConversationSpeakerAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name        string
		segments    []speech.Segment
		contextOnly bool
		uncertain   bool
	}{
		{name: "self", segments: []speech.Segment{{Role: speech.Self, Text: "Hey glasses, what is two plus two?"}}},
		{name: "unknown", segments: []speech.Segment{{Role: speech.Unknown, Text: "Hey glasses, what is two plus two?"}}},
		{name: "unlabeled"},
		{name: "conflicting_labels", contextOnly: true, uncertain: true, segments: []speech.Segment{{Role: speech.Unknown, Text: "Hey glasses, what is two plus two?"}}},
		{name: "other", contextOnly: true, segments: []speech.Segment{{Role: speech.Other, Text: "Hey glasses, what is two plus two?"}}},
		{name: "self_then_other", contextOnly: true, segments: []speech.Segment{
			{Role: speech.Self, Text: "Hey glasses, what is two plus two?"},
			{Role: speech.Other, Text: "Okay."},
		}},
		{name: "other_then_self", contextOnly: true, segments: []speech.Segment{
			{Role: speech.Other, Text: "Okay."},
			{Role: speech.Self, Text: "Hey glasses, what is two plus two?"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &conversationTranscriber{words: make(chan stt.Utterance, 2), stopped: make(chan struct{})}
			writer := conversationWriter{messages: make(chan serverMessage, 20)}
			handled := 0
			s := NewServer(fake, Handlers{ConversationTranscriber: fake, Utterance: func(_ context.Context, scope tool.Scope, _ string) (UtteranceResult, error) {
				if scope.Speech.ContextOnly() != tc.contextOnly {
					t.Error("speaker attribution was removed to bypass the gate")
				}
				handled++
				return UtteranceResult{Text: "Four."}, nil
			}})
			s.conversationIdle = 10 * time.Millisecond
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			text := "Hey glasses, what is two plus two?"
			if tc.name == "self_then_other" {
				text += " Okay."
			}
			if tc.name == "other_then_self" {
				text = "Okay. " + text
			}
			fake.words <- stt.Utterance{Text: text, Segments: tc.segments, SpeakerUncertain: tc.uncertain}
			fake.words <- stt.Utterance{Text: "And three plus three?", Segments: tc.segments, SpeakerUncertain: tc.uncertain}
			if err := s.runConversation(ctx, tool.Scope{}, writer, nil, true); err != nil {
				t.Fatal(err)
			}
			close(writer.messages)
			var events []string
			answered := false
			for message := range writer.messages {
				events = append(events, message.Type)
				answered = answered || message.Type == assistantResponseMessageType
			}
			if handled != 2 || !answered {
				t.Fatalf("wake and wake-free follow-up must reach the agent: handled=%d events=%v", handled, events)
			}
		})
	}
}
