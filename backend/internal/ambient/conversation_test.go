package ambient

import (
	"context"
	"encoding/binary"
	"errors"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"testing"
	"time"
)

type conversationFactory struct{ stream *conversationStream }

func (f conversationFactory) Open() (Stream, error) { return f.stream, nil }

type conversationStream struct {
	samples int
	reset   chan struct{}
}

func (s *conversationStream) Add(pcm []float32) error { s.samples += len(pcm); return nil }
func (s *conversationStream) Transcript() ([]Line, error) {
	if s.samples == 20*SampleRate {
		return []Line{{ID: 1, Text: "glasses", Start: 19, Complete: false}}, nil
	}
	return nil, nil
}
func (s *conversationStream) Reset() error {
	s.samples = 0
	if s.reset != nil {
		s.reset <- struct{}{}
	}
	return nil
}
func (*conversationStream) Close() {}

func TestStreamingHandoffReplaysOnceAndContinuesPastClipLimit(t *testing.T) {
	stream := &conversationStream{}
	listener := Listener{Factory: conversationFactory{stream}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	input := make(chan Input)
	done := make(chan error, 1)
	received := make(chan []byte, 1)
	go func() {
		done <- listener.RunStreaming(ctx, input, func(ctx context.Context, audio <-chan stt.AudioInput, automatic bool) error {
			if !automatic {
				t.Error("keyword did not authorize automatic handoff")
			}
			var all []byte
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case pcm := <-audio:
					all = append(all, pcm.PCM...)
					clear(pcm.PCM)
					if len(all) == 56*SampleRate*2 {
						received <- all
					}
				}
			}
		})
	}()
	for second := 0; second < 65; second++ {
		pcm := make([]byte, SampleRate*2)
		for i := 0; i < SampleRate; i++ {
			binary.LittleEndian.PutUint16(pcm[i*2:], uint16((second*SampleRate+i)%30000))
		}
		select {
		case input <- Input{PCM: pcm}:
		case <-ctx.Done():
			t.Fatal("listener stalled")
		}
	}
	select {
	case pcm := <-received:
		for i := 0; i < len(pcm)/2; i++ {
			if binary.LittleEndian.Uint16(pcm[i*2:]) != uint16((9*SampleRate+i)%30000) {
				t.Fatalf("audio duplicated or dropped at sample %d", i)
			}
		}
	case <-ctx.Done():
		t.Fatal("conversation truncated at the old clip limit")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if stream.samples != 20*SampleRate {
		t.Fatal("Moonshine kept decoding during active conversation")
	}
}

func TestManualFinalizeFlushesPartialBlockAndKeepsConnection(t *testing.T) {
	stream := &conversationStream{reset: make(chan struct{}, 1)}
	listener := Listener{Factory: conversationFactory{stream}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	input := make(chan Input)
	done := make(chan error, 1)
	frames := make(chan stt.AudioInput, 4)
	go func() {
		done <- listener.RunStreaming(ctx, input, func(ctx context.Context, audio <-chan stt.AudioInput, automatic bool) error {
			if automatic {
				t.Error("tap should not require a wake phrase")
			}
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case pcm := <-audio:
					frames <- pcm
				}
			}
		})
	}()
	input <- Input{Control: "listening_start"}
	input <- Input{PCM: []byte{1, 0, 2, 0}}
	input <- Input{Control: "conversation_finalize"}
	select {
	case pcm := <-frames:
		if len(pcm.PCM) != 4 || pcm.PCM[0] != 1 || pcm.PCM[2] != 2 {
			t.Fatalf("partial frame lost: %v", pcm)
		}
	case <-ctx.Done():
		t.Fatal("missing partial PCM")
	}
	select {
	case pcm := <-frames:
		if !pcm.Finalize {
			t.Fatal("expected finalize signal")
		}
	case <-ctx.Done():
		t.Fatal("missing finalize")
	}
	input <- Input{PCM: make([]byte, step*2)}
	select {
	case pcm := <-frames:
		if len(pcm.PCM) != step*2 {
			t.Fatal("stream closed at finalization")
		}
	case <-ctx.Done():
		t.Fatal("stream did not continue")
	}
	input <- Input{Control: "conversation_stop"}
	select {
	case <-stream.reset:
	case <-ctx.Done():
		t.Fatal("did not return to native listening")
	}
	cancel()
	<-done
}

func TestStreamingShutdownClearsQueuedAudioAndJoinsConversation(t *testing.T) {
	stream := &conversationStream{}
	listener := Listener{Factory: conversationFactory{stream}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := make(chan Input, 4)
	pcm := []byte{1, 0, 2, 0}
	queued := []byte{3, 0, 4, 0}
	input <- Input{Control: "listening_start"}
	input <- Input{PCM: pcm}
	input <- Input{Control: "conversation_finalize"}
	input <- Input{PCM: queued}
	joined := false
	err := listener.RunStreaming(ctx, input, func(ctx context.Context, audio <-chan stt.AudioInput, _ bool) error {
		frame := <-audio
		if len(frame.PCM) != 4 || frame.PCM[0] != 1 {
			t.Error("finalize overtook partial PCM")
		}
		clear(frame.PCM)
		cancel()
		<-ctx.Done()
		joined = true
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) || !joined {
		t.Fatalf("err=%v joined=%v", err, joined)
	}
	for _, audio := range [][]byte{pcm, queued} {
		for _, value := range audio {
			if value != 0 {
				t.Fatal("PCM retained after shutdown")
			}
		}
	}
}
