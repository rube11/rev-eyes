package stt

import (
	"context"
	msginterfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/api/listen/v1/websocket/interfaces"
	"github.com/rube11/rev-eyes/backend/internal/speech"
	"math"
	"reflect"
	"testing"
)

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
	if !u.ContextOnly() || u.PersonalMemory() || len(u.Segments) != 2 {
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
