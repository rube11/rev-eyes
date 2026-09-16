package ambient

import (
	"context"
	"encoding/binary"
	"errors"
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
		done <- listener.RunStreaming(ctx, input, func(ctx context.Context, audio <-chan []byte, automatic bool) error {
			if !automatic {
				t.Error("keyword did not authorize automatic handoff")
			}
			var all []byte
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case pcm := <-audio:
					all = append(all, pcm...)
					clear(pcm)
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
	frames := make(chan []byte, 4)
	go func() {
		done <- listener.RunStreaming(ctx, input, func(ctx context.Context, audio <-chan []byte, automatic bool) error {
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
		if len(pcm) != 4 || pcm[0] != 1 || pcm[2] != 2 {
			t.Fatalf("partial frame lost: %v", pcm)
		}
	case <-ctx.Done():
		t.Fatal("missing partial PCM")
	}
	select {
	case pcm := <-frames:
		if pcm != nil {
			t.Fatal("expected finalize signal")
		}
	case <-ctx.Done():
		t.Fatal("missing finalize")
	}
	input <- Input{PCM: make([]byte, step*2)}
	select {
	case pcm := <-frames:
		if len(pcm) != step*2 {
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
