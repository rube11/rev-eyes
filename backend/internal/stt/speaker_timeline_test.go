package stt

import (
	"context"
	"fmt"
	msginterfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/api/listen/v1/websocket/interfaces"
	"github.com/rube11/rev-eyes/backend/internal/speech"
	"math"
	"reflect"
	"testing"
)

func (t *speakerTimeline) role(start, end float64) speech.Role {
	role, _ := t.evidence(start, end)
	return role
}

func TestSpeakerAttributionAcrossEndpointsDoesNotCloseStream(t *testing.T) {
	completed := make(chan Utterance, 4)
	h := newDeepgramHandler(context.Background(), completed, func(string) error { return nil }, true)
	// This includes initial replay. Word timestamps are relative to exactly the
	// PCM submitted, not the original microphone or latest SDK frame.
	h.recordAudio(AudioInput{PCM: make([]byte, 4*speakerSampleRate*2), Speakers: []speech.Span{
		{Start: 0, End: speakerSampleRate, Role: speech.Self},
		{Start: speakerSampleRate, End: 2 * speakerSampleRate, Role: speech.Other},
		{Start: 3 * speakerSampleRate, End: 4 * speakerSampleRate, Role: speech.Self},
	}})
	for i, role := range []speech.Role{speech.Self, speech.Other, speech.Unknown, speech.Self} {
		m := deepgramMessage("hello", true, true, false)
		m.Channel.Alternatives[0].Words = []msginterfaces.Word{{Word: "hello", Start: float64(i) + .1, End: float64(i) + .5}}
		if err := h.Message(m); err != nil {
			t.Fatal(err)
		}
		got := <-completed
		if !reflect.DeepEqual(got.Segments, []speech.Segment{{Role: role, Text: "hello"}}) {
			t.Fatalf("turn %d: %+v", i, got)
		}
		select {
		case <-h.Endpointed():
			t.Fatal("speaker change closed stream")
		default:
		}
	}
}

func TestMixedSpeechAndMissingTimingStayConservative(t *testing.T) {
	completed := make(chan Utterance, 2)
	h := newDeepgramHandler(context.Background(), completed, func(string) error { return nil }, true)
	h.recordAudio(AudioInput{PCM: make([]byte, 2*speakerSampleRate*2), Speakers: []speech.Span{
		{End: speakerSampleRate, Role: speech.Self}, {Start: speakerSampleRate, End: 2 * speakerSampleRate, Role: speech.Other},
	}})
	m := deepgramMessage("Hello friend", true, true, false)
	m.Channel.Alternatives[0].Words = []msginterfaces.Word{{Word: "Hello", Start: .1, End: .4}, {Word: "friend", Start: 1.1, End: 1.5}}
	if err := h.Message(m); err != nil {
		t.Fatal(err)
	}
	u := <-completed
	if !u.ContextOnly() || len(u.Segments) != 2 {
		t.Fatalf("mixed utterance: %+v", u)
	}
	m = deepgramMessage("yes confirm it", true, true, false)
	m.Start, m.Duration = 1, .8
	if err := h.Message(m); err != nil {
		t.Fatal(err)
	}
	u = <-completed
	if !u.ContextOnly() {
		t.Fatal("missing words promoted other speaker into a command")
	}
	for _, pair := range [][2]float64{{-.1, .1}, {0, math.NaN()}, {0, math.Inf(1)}, {0, 4}, {.5, .5}} {
		if h.speakers.role(pair[0], pair[1]) != speech.Unknown {
			t.Fatal("invalid timestamps got a speaker")
		}
	}
}

func TestSpeakerTimelineIsBoundedAndDoesNotReuseStaleLabels(t *testing.T) {
	var timeline speakerTimeline
	timeline.add(AudioInput{PCM: make([]byte, 2*speakerRetention), Speakers: []speech.Span{{End: speakerRetention, Role: speech.Self}}})
	timeline.add(AudioInput{PCM: make([]byte, 2*speakerSampleRate)})
	if len(timeline.roles) != speakerRetention || timeline.role(0, .1) != speech.Unknown || timeline.role(120, 120.1) != speech.Unknown || timeline.role(119, 119.1) != speech.Self {
		t.Fatal("stale timeline attribution")
	}
}

func TestSpeakerGlitchesDoNotInventPeople(t *testing.T) {
	for _, samples := range []int64{0, 1, 320, 1600} {
		t.Run(fmt.Sprintf("other_samples_%d", samples), func(t *testing.T) {
			completed := make(chan Utterance, 2)
			h := newDeepgramHandler(context.Background(), completed, func(string) error { return nil }, true)
			spans := []speech.Span{{End: 32000, Role: speech.Self}}
			if samples > 0 {
				spans = append(spans, speech.Span{Start: 7200, End: 7200 + samples, Role: speech.Other})
			}
			h.recordAudio(AudioInput{PCM: make([]byte, 64000), Speakers: spans})
			m := deepgramMessage("I need help with this.", true, true, false)
			m.Channel.Alternatives[0].Words = []msginterfaces.Word{
				{Word: "I", Start: .1, End: .3}, {Word: "need", Start: .4, End: .8},
				{Word: "help", Start: .9, End: 1.2}, {Word: "with", Start: 1.3, End: 1.5},
				{Word: "this", PunctuatedWord: "this.", Start: 1.6, End: 1.9},
			}
			if err := h.Message(m); err != nil {
				t.Fatal(err)
			}
			if len(completed) != 1 {
				t.Fatalf("completed %d utterances", len(completed))
			}
			u := <-completed
			want := speech.Self
			if samples > 0 {
				want = speech.Unknown
			}
			if !reflect.DeepEqual(u.Segments, []speech.Segment{{Role: want, Text: u.Text}}) {
				t.Fatalf("brief glitch fragmented one sentence: %s", u.Record())
			}
			if samples > 0 && !u.ContextOnly() {
				t.Fatal("uncertain attribution must not authorize proposal confirmation")
			}
			// Uncertainty must not leak into the next utterance on this stream.
			m = deepgramMessage("hello", true, true, false)
			m.Channel.Alternatives[0].Words = []msginterfaces.Word{{Word: "hello", Start: 1.1, End: 1.5}}
			if err := h.Message(m); err != nil {
				t.Fatal(err)
			}
			u = <-completed
			if u.ContextOnly() || u.SpeakerUncertain {
				t.Fatalf("uncertainty leaked: %+v", u)
			}
		})
	}
}

func TestSpeakerEvidenceRequiresSustainedLabels(t *testing.T) {
	for _, role := range []speech.Role{speech.Self, speech.Other} {
		var timeline speakerTimeline
		timeline.add(AudioInput{PCM: make([]byte, 32000), Speakers: []speech.Span{{Start: 7200, End: 7520, Role: role}}})
		if got := timeline.role(.45, .47); got != speech.Unknown {
			t.Fatalf("20ms island confidently attributed to %s", got)
		}
		var stable speakerTimeline
		stable.add(AudioInput{PCM: make([]byte, 32000), Speakers: []speech.Span{{End: 16000, Role: role}}})
		if got := stable.role(.45, .47); got != role {
			t.Fatalf("short word in sustained speech: %s", got)
		}
	}
}

func TestSustainedSpeakerChangesSurviveUncertainBoundary(t *testing.T) {
	completed := make(chan Utterance, 1)
	h := newDeepgramHandler(context.Background(), completed, func(string) error { return nil }, true)
	// Separate audio frames, on a single continuous connection.
	h.recordAudio(AudioInput{PCM: make([]byte, 32000), Speakers: []speech.Span{{End: 16000, Role: speech.Self}}})
	h.recordAudio(AudioInput{PCM: make([]byte, 32000), Speakers: []speech.Span{{End: 16000, Role: speech.Other}}})
	m := deepgramMessage("Hello there friend", true, true, false)
	m.Channel.Alternatives[0].Words = []msginterfaces.Word{
		{Word: "Hello", Start: .1, End: .4}, {Word: "there", Start: .9, End: 1.1}, {Word: "friend", Start: 1.2, End: 1.5},
	}
	if err := h.Message(m); err != nil {
		t.Fatal(err)
	}
	u := <-completed
	want := []speech.Segment{{Role: speech.Self, Text: "Hello"}, {Role: speech.Unknown, Text: "there"}, {Role: speech.Other, Text: "friend"}}
	if !reflect.DeepEqual(u.Segments, want) || !u.ContextOnly() {
		t.Fatalf("lost real speaker change: %+v", u)
	}
}

func TestSpeakerGlitchStressAcrossStreamingFinals(t *testing.T) {
	completed := make(chan Utterance, 1)
	h := newDeepgramHandler(context.Background(), completed, func(string) error { return nil }, true)
	for i := 0; i < 1000; i++ {
		base, glitch := speech.Self, speech.Other
		if i%2 != 0 {
			base, glitch = glitch, base
		}
		start := int64(3200 + (i*3571)%3000)
		length := int64(1 + (i*859)%2000)
		// Vary glitch length and placement; exercise both role directions and
		// more than 120s of audio so the bounded timeline wraps repeatedly.
		h.recordAudio(AudioInput{PCM: make([]byte, 32000), Speakers: []speech.Span{
			{End: 16000, Role: base}, {Start: start, End: start + length, Role: glitch},
		}})
		offset := float64(2 * i)
		for j, word := range []msginterfaces.Word{
			{Word: "Hello", Start: offset + .1, End: offset + .6},
			{Word: "friend", Start: offset + .65, End: offset + .95},
		} {
			m := deepgramMessage(word.Word, true, j == 1, false)
			m.Channel.Alternatives[0].Words = []msginterfaces.Word{word}
			if err := h.Message(m); err != nil {
				t.Fatal(err)
			}
			if j == 0 && len(completed) != 0 {
				t.Fatal("speaker evidence ended utterance early")
			}
		}
		u := <-completed
		if u.Text != "Hello friend" || len(u.Segments) != 1 || u.Segments[0].Role != speech.Unknown || !u.ContextOnly() {
			t.Fatalf("glitch %d: %+v", i, u)
		}
		h.recordAudio(AudioInput{PCM: make([]byte, 32000), Speakers: []speech.Span{{End: 16000, Role: base}}})
		m := deepgramMessage("Next", true, true, false)
		m.Channel.Alternatives[0].Words = []msginterfaces.Word{{Word: "Next", Start: offset + 1.1, End: offset + 1.6}}
		if err := h.Message(m); err != nil {
			t.Fatal(err)
		}
		u = <-completed
		if len(u.Segments) != 1 || u.Segments[0].Role != base || u.SpeakerUncertain {
			t.Fatalf("clean utterance %d contaminated: %+v", i, u)
		}
	}
}
