package realtime

import (
	"errors"
	"fmt"
	"strings"

	"github.com/rube11/rev-eyes/backend/internal/stt"
)

const (
	candidateAudioMessageType      = "candidate_audio"
	candidateEncoding              = stt.EncodingLinear16
	candidateSampleRate            = 16_000
	candidateChannels              = 1
	candidateBytesPerSample        = 2
	candidateMaxDurationSeconds    = 30
	candidateMaxSamples            = candidateSampleRate * candidateMaxDurationSeconds
	maxCandidateAudioBytes         = candidateMaxSamples * candidateChannels * candidateBytesPerSample
	recentCandidateIDCapacity      = 256
	maxCandidateIDLength           = 64
	candidateAssistantDoneFallback = "assistant_done"
)

var errCandidateHeaderInvalid = errors.New("candidate audio header is invalid")

type candidateAudioHeader struct {
	ID                string  `json:"id"`
	Encoding          string  `json:"encoding"`
	SampleRate        int     `json:"sample_rate"`
	Channels          int     `json:"channels"`
	ByteLength        int     `json:"byte_length"`
	StartSampleOffset int64   `json:"start_sample_offset"`
	EndSampleOffset   int64   `json:"end_sample_offset"`
	GateCategory      string  `json:"gate_category,omitempty"`
	GateConfidence    float64 `json:"gate_confidence,omitempty"`
}

type connectionAudioMode uint8

const (
	audioModeUnset connectionAudioMode = iota
	audioModeLegacy
	audioModeCandidate
)

// candidateIDWindow bounds duplicate detection without eventually exhausting a
// long-lived ambient connection.
type candidateIDWindow struct {
	seen  map[string]struct{}
	order [recentCandidateIDCapacity]string
	next  int
	size  int
}

func newCandidateIDWindow() *candidateIDWindow {
	return &candidateIDWindow{seen: make(map[string]struct{}, recentCandidateIDCapacity)}
}

func (window *candidateIDWindow) Contains(id string) bool {
	if window == nil {
		return false
	}
	_, exists := window.seen[id]
	return exists
}

func (window *candidateIDWindow) Add(id string) {
	if window == nil || window.Contains(id) {
		return
	}
	if window.size == recentCandidateIDCapacity {
		delete(window.seen, window.order[window.next])
	} else {
		window.size++
	}
	window.order[window.next] = id
	window.next = (window.next + 1) % recentCandidateIDCapacity
	window.seen[id] = struct{}{}
}

func (header candidateAudioHeader) validate() error {
	if header.ID != strings.TrimSpace(header.ID) || !validCandidateID(header.ID) {
		return fmt.Errorf("%w: invalid id", errCandidateHeaderInvalid)
	}
	if header.Encoding != candidateEncoding ||
		header.SampleRate != candidateSampleRate ||
		header.Channels != candidateChannels {
		return fmt.Errorf("%w: unsupported format", errCandidateHeaderInvalid)
	}
	if header.ByteLength <= 0 || header.ByteLength > maxCandidateAudioBytes {
		return fmt.Errorf("%w: invalid byte length", errCandidateHeaderInvalid)
	}
	if header.StartSampleOffset < 0 ||
		header.EndSampleOffset <= header.StartSampleOffset {
		return fmt.Errorf("%w: invalid sample range", errCandidateHeaderInvalid)
	}
	samples := header.EndSampleOffset - header.StartSampleOffset
	if samples > candidateMaxSamples {
		return fmt.Errorf("%w: candidate exceeds duration limit", errCandidateHeaderInvalid)
	}
	expectedBytes := samples * candidateChannels * candidateBytesPerSample
	if expectedBytes != int64(header.ByteLength) {
		return fmt.Errorf("%w: byte length does not match sample range", errCandidateHeaderInvalid)
	}
	if header.GateCategory != "" && !validGateCategory(header.GateCategory) {
		return fmt.Errorf("%w: invalid gate category", errCandidateHeaderInvalid)
	}
	if header.GateConfidence < 0 || header.GateConfidence > 1 {
		return fmt.Errorf("%w: invalid gate confidence", errCandidateHeaderInvalid)
	}
	return nil
}

func (header candidateAudioHeader) format() stt.AudioFormat {
	return stt.AudioFormat{
		Encoding:   header.Encoding,
		SampleRate: header.SampleRate,
		Channels:   header.Channels,
	}
}

func validCandidateID(id string) bool {
	if id == "" || len(id) > maxCandidateIDLength {
		return false
	}
	for _, character := range id {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func validGateCategory(category string) bool {
	switch category {
	case "assistant_request", "commitment", "intention", "manual", "preference", "reminder":
		return true
	default:
		return false
	}
}

func clearCandidateAudio(audio []byte) {
	for index := range audio {
		audio[index] = 0
	}
}
