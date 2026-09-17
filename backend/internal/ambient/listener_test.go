package ambient

import (
	"context"
	"encoding/binary"
	"fmt"
	"github.com/rube11/rev-eyes/backend/internal/candidate"
	"testing"
	"time"
)

type fakeStream struct {
	lines   []Line
	resets  int
	samples int
	closed  bool
}

func (s *fakeStream) Add(pcm []float32) error     { s.samples += len(pcm); return nil }
func (s *fakeStream) Transcript() ([]Line, error) { lines := s.lines; s.lines = nil; return lines, nil }
func (s *fakeStream) Reset() error                { s.resets++; return nil }
func (s *fakeStream) Close()                      { s.closed = true }
func advance(t *testing.T, d *detector, seconds int) {
	t.Helper()
	for i := 0; i < seconds*4; i++ {
		for j := 0; j < step; j++ {
			d.ring[d.end%retention] = int16(d.end % 30000)
			d.end++
		}
		if err := d.process(make([]float32, step)); err != nil {
			t.Fatal(err)
		}
	}
}
func TestKeywordClipIncludesPrerollAndIsNotDuplicated(t *testing.T) {
	s := &fakeStream{}
	var clips []Clip
	d := detector{stream: s, seen: map[uint64]bool{}, emit: func(c Clip) { clips = append(clips, c) }}
	advance(t, &d, 20)
	s.lines = []Line{{ID: 1, Text: "glasses remind me tomorrow", Start: 19, Duration: 1, Complete: true}}
	advance(t, &d, 3)
	if len(clips) != 1 {
		t.Fatalf("clips=%d", len(clips))
	}
	c := clips[0]
	if c.Start != 9*SampleRate || c.End != 22*SampleRate || c.Reason != candidate.WakeReminder {
		t.Fatalf("bad clip window: %d..%d %s", c.Start, c.End, c.Reason)
	}
	if int16(binary.LittleEndian.Uint16(c.Audio)) != int16(c.Start%30000) {
		t.Fatal("ring sample ordering lost")
	}
	s.lines = []Line{{ID: 1, Text: "glasses remind me tomorrow", Start: 19, Duration: 1, Complete: true}}
	advance(t, &d, 5)
	if len(clips) != 1 {
		t.Fatal("duplicate transcript triggered twice")
	}
}
func TestBackgroundSpeechNeverEmitsAndStreamsRotate(t *testing.T) {
	s := &fakeStream{lines: []Line{{ID: 1, Text: "the weather is nice", Start: 0, Duration: 1, Complete: true}}}
	d := detector{stream: s, seen: map[uint64]bool{}, emit: func(Clip) { t.Fatal("background clip emitted") }}
	advance(t, &d, 61)
	if s.resets != 1 || d.base != 55*SampleRate {
		t.Fatalf("stream not rotated: %d %d", s.resets, d.base)
	}
}
func TestLongRequestIsBounded(t *testing.T) {
	s := &fakeStream{lines: []Line{{ID: 1, Text: "glasses", Start: 0, Duration: .25}}}
	var clips []Clip
	d := detector{stream: s, seen: map[uint64]bool{}, emit: func(c Clip) { clips = append(clips, c) }}
	advance(t, &d, 31)
	if len(clips) != 1 || len(clips[0].Audio) != retention*2 {
		t.Fatal("long request not bounded to retention")
	}
}

type fakeFactory struct{ s *fakeStream }

func (f fakeFactory) Open() (Stream, error) { return f.s, nil }
func TestMalformedAudioClearedAndStreamClosed(t *testing.T) {
	s := &fakeStream{}
	l := Listener{Factory: fakeFactory{s}}
	pcm := []byte{1, 2, 3}
	input := make(chan Input, 1)
	input <- Input{PCM: pcm}
	err := l.Run(context.Background(), input, func(Clip) { t.Fatal("unexpected clip") })
	if err == nil || !s.closed {
		t.Fatal("invalid audio did not release stream")
	}
	for _, b := range pcm {
		if b != 0 {
			t.Fatal("audio retained")
		}
	}
}
func TestCanceledSessionDoesNotFlush(t *testing.T) {
	s := &fakeStream{}
	l := Listener{Factory: fakeFactory{s}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.Run(ctx, nil, func(Clip) { t.Fatal("canceled clip emitted") }); err != context.Canceled || !s.closed {
		t.Fatal("cancellation did not close stream")
	}
}
func TestManualFlushDoesNotRequireKeyword(t *testing.T) {
	s := &fakeStream{}
	var clip Clip
	d := detector{stream: s, seen: map[uint64]bool{}, pending: true, reason: candidate.WakeManual, emit: func(c Clip) { clip = c }}
	advance(t, &d, 1)
	d.flush()
	if clip.Reason != candidate.WakeManual || len(clip.Audio) != 2*SampleRate {
		t.Fatal("manual clip missing")
	}
}

func TestManualControlsFollowQueuedAudio(t *testing.T) {
	s := &fakeStream{}
	listener := Listener{Factory: fakeFactory{s}}
	input := make(chan Input, 4)
	input <- Input{Control: "listening_start"}
	pcm := make([]byte, SampleRate*2)
	for i := range pcm {
		pcm[i] = 1
	}
	input <- Input{PCM: pcm}
	input <- Input{Control: "listening_stop"}
	close(input)
	var clips []Clip
	if err := listener.Run(context.Background(), input, func(c Clip) { clips = append(clips, c) }); err != nil {
		t.Fatal(err)
	}
	if len(clips) != 1 || len(clips[0].Audio) != SampleRate*2 {
		t.Fatal("stop overtook queued audio")
	}
}

func TestReplyAuthorizationExpiresAndConsumesOnce(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprint(expired), func(t *testing.T) {
			s := &fakeStream{}
			var clips []Clip
			d := detector{stream: s, seen: map[uint64]bool{}, emit: func(c Clip) { clips = append(clips, c) }}
			d.replyUntil = time.Now().Add(time.Minute)
			if expired {
				d.replyUntil = time.Now().Add(-time.Second)
			}
			s.lines = []Line{{ID: 1, Text: "yes please", Start: 0, Duration: .25, Complete: true}}
			advance(t, &d, 3)
			s.lines = []Line{{ID: 2, Text: "something unrelated", Start: 3, Duration: .25, Complete: true}}
			advance(t, &d, 3)
			want := 1
			if expired {
				want = 0
			}
			if len(clips) != want {
				t.Fatalf("clips=%d want %d", len(clips), want)
			}
			if want == 1 && clips[0].Reason != candidate.WakeManual {
				t.Fatal("reply not authorized as manual")
			}
		})
	}
}

func TestTapFinishesReplyBeforeOrAfterRoughRecognition(t *testing.T) {
	for _, recognized := range []bool{false, true} {
		t.Run(fmt.Sprint(recognized), func(t *testing.T) {
			s := &fakeStream{}
			if recognized {
				s.lines = []Line{{ID: 1, Text: "yes please", Start: 0, Duration: .25}}
			}
			listener := Listener{Factory: fakeFactory{s}}
			input := make(chan Input, 4)
			input <- Input{Control: "ambient_reply_arm"}
			input <- Input{PCM: make([]byte, 2*SampleRate)}
			input <- Input{Control: "listening_stop"}
			input <- Input{Control: "ambient_reply_disarm"}
			close(input)
			var clips []Clip
			if err := listener.Run(context.Background(), input, func(c Clip) { clips = append(clips, c) }); err != nil {
				t.Fatal(err)
			}
			if len(clips) != 1 || clips[0].Reason != candidate.WakeManual || len(clips[0].Audio) != 2*SampleRate {
				t.Fatalf("tap lost or delayed reply: %d clips", len(clips))
			}
		})
	}
}
