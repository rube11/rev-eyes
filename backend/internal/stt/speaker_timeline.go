package stt

import (
	msginterfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/api/listen/v1/websocket/interfaces"
	"github.com/rube11/rev-eyes/backend/internal/speech"
	"math"
	"strings"
)

const speakerSampleRate = 16000
const speakerRetention = 120 * speakerSampleRate

// A starting heuristic, not calibrated identity confidence. A short word may
// borrow support from an adjacent same-role run, but we never wait for audio.
const minimumSpeakerRun = 150 * speakerSampleRate / 1000

// One bounded timeline per Deepgram connection, including replayed samples.
// Access is protected by the handler mutex. Speaker changes never finalize,
// reconnect, or change the audio sent to Deepgram.
type speakerTimeline struct {
	roles []speech.Role
	end   int64
}

func (t *speakerTimeline) add(input AudioInput) {
	if t.roles == nil {
		t.roles = make([]speech.Role, speakerRetention)
	}
	n := int64(len(input.PCM) / 2)
	for i := int64(0); i < n; i++ {
		t.roles[(t.end+i)%speakerRetention] = speech.Unknown
	}
	for _, span := range input.Speakers {
		if span.Role != speech.Self && span.Role != speech.Other {
			continue
		}
		for i := max(int64(0), span.Start); i < min(n, span.End); i++ {
			t.roles[(t.end+i)%speakerRetention] = span.Role
		}
	}
	t.end += n
}

func (t *speakerTimeline) evidence(start, end float64) (speech.Role, bool) {
	if math.IsNaN(start) || math.IsNaN(end) || math.IsInf(start, 0) || math.IsInf(end, 0) || start < 0 || end <= start || end > float64(t.end)/speakerSampleRate {
		return speech.Unknown, false
	}
	a, b := int64(math.Floor(start*speakerSampleRate)), int64(math.Ceil(end*speakerSampleRate))
	if a < t.end-speakerRetention || b <= a || len(t.roles) == 0 {
		return speech.Unknown, false
	}
	role := t.roles[a%speakerRetention]
	other := false
	conflicting := false
	for i := a; i < b; i++ {
		r := t.roles[i%speakerRetention]
		other = other || r == speech.Other
		conflicting = conflicting || r != role
	}
	if conflicting || role == speech.Unknown {
		return speech.Unknown, other
	}
	// Confirm a continuous run, including support outside very short words.
	// Do not carry a role across gaps, stream restarts, or overwritten samples.
	left, right := a, b
	for left > max(int64(0), t.end-speakerRetention) && right-left < minimumSpeakerRun && t.roles[(left-1)%speakerRetention] == role {
		left--
	}
	for right < t.end && right-left < minimumSpeakerRun && t.roles[right%speakerRetention] == role {
		right++
	}
	if right-left < minimumSpeakerRun {
		return speech.Unknown, other
	}
	return role, other
}

func (h *deepgramHandler) recordAudio(input AudioInput) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.speakers.add(input)
}

func (h *deepgramHandler) attribute(alternative msginterfaces.Alternative, start, duration float64) {
	if len(alternative.Words) == 0 {
		// Missing word timing must not turn known third-party audio into an
		// unattributed command. Still require word timing for personal memories.
		role, other := h.speakers.evidence(start, start+duration)
		if role != speech.Other {
			role = speech.Unknown
		}
		h.speakerUncertain = h.speakerUncertain || (role == speech.Unknown && other)
		h.appendSegment(role, alternative.Transcript)
		return
	}
	for _, word := range alternative.Words {
		text := word.PunctuatedWord
		if text == "" {
			text = word.Word
		}
		role, other := h.speakers.evidence(word.Start, word.End)
		h.speakerUncertain = h.speakerUncertain || (role == speech.Unknown && other)
		h.appendSegment(role, text)
	}
}

func (h *deepgramHandler) appendSegment(role speech.Role, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	n := len(h.segments)
	if n > 0 && h.segments[n-1].Role == role {
		h.segments[n-1].Text += " " + text
	} else {
		h.segments = append(h.segments, speech.Segment{Role: role, Text: text})
	}
}
