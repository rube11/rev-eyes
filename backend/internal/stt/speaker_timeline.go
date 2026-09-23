package stt

import (
	msginterfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/api/listen/v1/websocket/interfaces"
	"github.com/rube11/rev-eyes/backend/internal/speech"
	"math"
	"strings"
)

const speakerSampleRate = 16000
const speakerRetention = 120 * speakerSampleRate

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

func (t *speakerTimeline) role(start, end float64) speech.Role {
	if math.IsNaN(start) || math.IsNaN(end) || math.IsInf(start, 0) || math.IsInf(end, 0) || start < 0 || end <= start || end > float64(t.end)/speakerSampleRate {
		return speech.Unknown
	}
	a, b := int64(math.Floor(start*speakerSampleRate)), int64(math.Ceil(end*speakerSampleRate))
	if a < t.end-speakerRetention || b <= a || len(t.roles) == 0 {
		return speech.Unknown
	}
	role := speech.Self
	for i := a; i < b; i++ {
		r := t.roles[i%speakerRetention]
		if r == speech.Other {
			return speech.Other
		}
		if r != speech.Self {
			role = speech.Unknown
		}
	}
	return role
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
		role := speech.Unknown
		if h.speakers.role(start, start+duration) == speech.Other {
			role = speech.Other
		}
		h.appendSegment(role, alternative.Transcript)
		return
	}
	for _, word := range alternative.Words {
		text := word.PunctuatedWord
		if text == "" {
			text = word.Word
		}
		h.appendSegment(h.speakers.role(word.Start, word.End), text)
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
