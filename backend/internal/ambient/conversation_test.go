package ambient

import (
	"context"
	"encoding/binary"
	"errors"
	"github.com/rube11/rev-eyes/backend/internal/speech"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"sync/atomic"
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

func TestStreamingAudioGapFlushesPartialPCMAndFeedsOneSilenceWindow(t *testing.T) {
	stream := &conversationStream{reset: make(chan struct{}, 4)}
	listener := Listener{Factory: conversationFactory{stream}}
	ctx, cancel := context.WithCancel(context.Background())
	input := make(chan Input)
	frames := make(chan stt.AudioInput, 8)
	done := make(chan error, 1)
	var calls atomic.Int32
	go func() {
		done <- listener.RunStreaming(ctx, input, func(ctx context.Context, audio <-chan stt.AudioInput, automatic bool) error {
			calls.Add(1)
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case frame := <-audio:
					frames <- frame
				}
			}
		})
	}()
	defer func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	}()
	input <- Input{Control: "listening_start"}
	// A device may stop emitting audio events at a pause instead of sending
	// silent PCM. Keep a partial block so the test also catches lost tail audio.
	input <- Input{PCM: []byte{1, 0, 2, 0}, Role: speech.Self}
	assertSilence := func(frame stt.AudioInput) {
		t.Helper()
		if len(frame.PCM) != int(conversationAudioGap*SampleRate/time.Second)*2 || len(frame.Speakers) != 0 {
			t.Fatalf("pause was not filled by one bounded unattributed silence window: bytes=%d speakers=%+v", len(frame.PCM), frame.Speakers)
		}
		for _, sample := range frame.PCM {
			if sample != 0 {
				t.Fatal("padding was not silence")
			}
		}
	}
	assertFlushed := func(want byte) {
		t.Helper()
		select {
		case frame := <-frames:
			if len(frame.PCM) != 4 || frame.PCM[0] != want || len(frame.Speakers) != 1 || frame.Speakers[0].Role != speech.Self {
				t.Fatalf("partial PCM was not preserved before finalization: %+v", frame)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no PCM arrived after the microphone paused")
		}
		select {
		case frame := <-frames:
			assertSilence(frame)
		case <-time.After(time.Second):
			t.Fatal("pause did not finalize the buffered utterance")
		}
	}
	assertFlushed(1)
	select {
	case frame := <-frames:
		t.Fatalf("audio gap produced repeated finalization: %+v", frame)
	case <-time.After(1400 * time.Millisecond):
	}
	// A manual finalize consumes the same pending audio and disarms the gap;
	// neither event may restart either recognizer.
	input <- Input{PCM: []byte{3, 0, 4, 0}, Role: speech.Self}
	input <- Input{Control: "conversation_finalize"}
	assertFlushed(3)
	// Even without new PCM, an explicit retry must reach the provider in case
	// its prior endpoint was lost. Only the automatic padding is one-shot.
	input <- Input{Control: "conversation_finalize"}
	select {
	case frame := <-frames:
		assertSilence(frame)
	case <-time.After(time.Second):
		t.Fatal("explicit finalize retry was suppressed")
	}
	input <- Input{Role: speech.Self}
	select {
	case frame := <-frames:
		t.Fatalf("manual finalize was duplicated by audio-gap fallback: %+v", frame)
	case <-time.After(1400 * time.Millisecond):
	}
	if calls.Load() != 1 || stream.samples != 0 || len(stream.reset) != 0 {
		t.Fatalf("pause restarted recognizers: conversations=%d moonshine samples=%d resets=%d", calls.Load(), stream.samples, len(stream.reset))
	}
}

func TestOtherLabeledWakeOpensConversationWithoutRestartingMoonshine(t *testing.T) {
	stream := &conversationStream{reset: make(chan struct{}, 1)}
	l := Listener{Factory: conversationFactory{stream}}
	input := make(chan Input, 20)
	for i := 0; i < 20; i++ {
		input <- Input{PCM: make([]byte, SampleRate*2), Role: speech.Other}
	}
	close(input)
	var opened atomic.Bool
	err := l.RunStreaming(context.Background(), input, func(ctx context.Context, _ <-chan stt.AudioInput, automatic bool) error {
		opened.Store(true)
		if !automatic {
			t.Error("keyword handoff was not automatic")
		}
		<-ctx.Done()
		return ctx.Err()
	})
	if err != nil || !opened.Load() || stream.samples != 20*SampleRate || len(stream.reset) != 0 {
		t.Fatalf("err=%v opened=%v samples=%d resets=%d", err, opened.Load(), stream.samples, len(stream.reset))
	}
}

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
					for _, span := range pcm.Speakers {
						for n := span.Start; n < span.End; n++ {
							second := (9*SampleRate + len(all)/2 + int(n)) / SampleRate
							if span.Role != speech.Role(second%3) {
								t.Errorf("replayed speaker drift at %d", second)
								break
							}
						}
					}
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
		case input <- Input{PCM: pcm, Role: speech.Role(second % 3)}:
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
		if len(pcm.PCM) != int(conversationAudioGap*SampleRate/time.Second)*2 {
			t.Fatal("expected one end-of-turn silence window, not a premature Finalize")
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
