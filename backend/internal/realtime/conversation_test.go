package realtime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type conversationTranscriber struct {
	calls   atomic.Int32
	words   chan string
	stopped chan struct{}
}

func (*conversationTranscriber) Transcribe(context.Context, <-chan stt.AudioInput, chan<- string, stt.TranscriptObserver) error {
	return errors.New("opened per-turn transcription")
}
func (f *conversationTranscriber) TranscribeConversation(ctx context.Context, _ <-chan stt.AudioInput, completed chan<- string, observe stt.TranscriptObserver) error {
	f.calls.Add(1)
	defer close(f.stopped)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case text := <-f.words:
			if err := observe(text); err != nil {
				return err
			}
			select {
			case completed <- text:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

type conversationWriter struct{ messages chan serverMessage }

func (w conversationWriter) WriteJSON(v any) error { w.messages <- v.(serverMessage); return nil }

func TestConversationReusesConnectionAcrossTurnsAndExpiresAfterResponse(t *testing.T) {
	fake := &conversationTranscriber{words: make(chan string, 4), stopped: make(chan struct{})}
	turns := make(chan string, 4)
	release := make(chan struct{})
	s := NewServer(fake, Handlers{ConversationTranscriber: fake, Utterance: func(ctx context.Context, _ tool.Scope, text string) (UtteranceResult, error) {
		turns <- text
		if text == "glasses hello" {
			select {
			case <-release:
			case <-ctx.Done():
				return UtteranceResult{}, ctx.Err()
			}
		}
		return UtteranceResult{Text: "answer: " + text}, nil
	}})
	s.conversationIdle = 80 * time.Millisecond
	writer := conversationWriter{messages: make(chan serverMessage, 30)}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.runConversation(ctx, tool.Scope{UserID: "u", SessionID: "s"}, writer, nil, true) }()
	fake.words <- "background television"
	fake.words <- "glasses hello"
	select {
	case text := <-turns:
		if text != "glasses hello" {
			t.Fatal("background pre-roll executed")
		}
	case <-ctx.Done():
		t.Fatal("no first turn")
	}
	// A slow response must not consume the user's follow-up allowance.
	select {
	case err := <-done:
		t.Fatalf("expired during response: %v", err)
	case <-time.After(2 * s.conversationIdle):
	}
	close(release)
	fake.words <- "and tomorrow?"
	select {
	case text := <-turns:
		if text != "and tomorrow?" {
			t.Fatal(text)
		}
	case <-ctx.Done():
		t.Fatal("follow-up incorrectly needed a wake phrase")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("idle did not close streaming")
	}
	if fake.calls.Load() != 1 {
		t.Fatal("opened a new Deepgram connection for follow-up")
	}
	select {
	case <-fake.stopped:
	default:
		t.Fatal("idle leaked the paid connection")
	}
	close(writer.messages)
	responses, idle := 0, 0
	for msg := range writer.messages {
		if msg.Type == assistantResponseMessageType {
			responses++
		}
		if msg.Type == "conversation_idle" {
			idle++
		}
	}
	if responses != 2 || idle != 1 {
		t.Fatalf("responses=%d idle=%d", responses, idle)
	}
}

func TestConversationCancellationJoinsStreamAndReleasesAdmission(t *testing.T) {
	fake := &conversationTranscriber{words: make(chan string), stopped: make(chan struct{})}
	s := NewServer(fake, Handlers{ConversationTranscriber: fake, CandidateAudio: func(context.Context, []byte, stt.AudioFormat) (string, error) { return "", nil }})
	writer := conversationWriter{messages: make(chan serverMessage, 4)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.runConversation(ctx, tool.Scope{}, writer, nil, false) }()
	select {
	case <-writer.messages:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("conversation did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation leaked conversation")
	}
	select {
	case <-fake.stopped:
	default:
		t.Fatal("paid connection outlived conversation")
	}
	if len(s.candidatePermits) != 0 {
		t.Fatal("paid stream permit leaked")
	}
}

func TestConversationWakeAuthorizationAndIdleDelivery(t *testing.T) {
	for _, automatic := range []bool{true, false} {
		t.Run(map[bool]string{true: "automatic", false: "manual"}[automatic], func(t *testing.T) {
			fake := &conversationTranscriber{words: make(chan string, 1), stopped: make(chan struct{})}
			turns := make(chan string, 1)
			s := NewServer(fake, Handlers{ConversationTranscriber: fake, Utterance: func(_ context.Context, _ tool.Scope, text string) (UtteranceResult, error) {
				turns <- text
				return UtteranceResult{}, nil
			}})
			s.conversationIdle = 30 * time.Millisecond
			writer := conversationWriter{messages: make(chan serverMessage, 10)}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- s.runConversation(ctx, tool.Scope{}, writer, nil, automatic) }()
			fake.words <- "The weather is nice."
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if automatic && len(turns) != 0 {
				t.Fatal("unapproved accurate speech executed")
			}
			if !automatic && len(turns) != 1 {
				t.Fatal("manual authorization required a keyword")
			}
			close(writer.messages)
			idle, thinking := 0, 0
			for message := range writer.messages {
				if message.Type == "conversation_idle" {
					idle++
				}
				if message.Type == assistantThinkingMessageType {
					thinking++
				}
			}
			if idle != 1 || (automatic && thinking != 0) || (!automatic && thinking != 1) {
				t.Fatalf("idle=%d thinking=%d automatic=%v", idle, thinking, automatic)
			}
			select {
			case <-fake.stopped:
			default:
				t.Fatal("idle delivered before paid worker joined")
			}
		})
	}
}
