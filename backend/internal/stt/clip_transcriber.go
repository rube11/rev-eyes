package stt

import (
	"context"
	"errors"
	"strings"
)

const EncodingLinear16 = "linear16"

var ErrAudioFormatInvalid = errors.New("audio format is invalid")

// AudioFormat describes a finite headerless PCM clip.
type AudioFormat struct {
	Encoding   string
	SampleRate int
	Channels   int
}

func (f AudioFormat) Validate() error {
	if strings.TrimSpace(f.Encoding) == "" || f.SampleRate <= 0 || f.Channels <= 0 {
		return ErrAudioFormatInvalid
	}
	return nil
}

// ClipTranscriber accurately transcribes one finite audio clip.
type ClipTranscriber interface {
	TranscribeClip(ctx context.Context, audio []byte, format AudioFormat) (string, error)
}
