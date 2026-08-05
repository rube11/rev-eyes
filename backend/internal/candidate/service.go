package candidate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/stt"
)

var (
	ErrTranscriberRequired = errors.New("candidate clip transcriber is required")
	ErrAudioRequired       = errors.New("candidate audio is required")
	ErrContextRequired     = errors.New("candidate context is required")
)

const defaultTranscriptionTimeout = 20 * time.Second

// Service transcribes one finite clip and owns clearing the supplied audio slice.
type Service struct {
	transcriber stt.ClipTranscriber
	timeout     time.Duration
}

func NewService(transcriber stt.ClipTranscriber) (*Service, error) {
	if transcriber == nil {
		return nil, ErrTranscriberRequired
	}
	return &Service{
		transcriber: transcriber,
		timeout:     defaultTranscriptionTimeout,
	}, nil
}

// Process transfers the clip to the transcriber and clears it on every exit path.
func (s *Service) Process(
	ctx context.Context,
	audio []byte,
	format stt.AudioFormat,
) (string, error) {
	defer clearBytes(audio)
	if ctx == nil {
		return "", ErrContextRequired
	}
	if len(audio) == 0 {
		return "", ErrAudioRequired
	}
	if s == nil || s.transcriber == nil {
		return "", ErrTranscriberRequired
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := format.Validate(); err != nil {
		return "", err
	}
	if s.timeout <= 0 {
		return "", errors.New("candidate transcription timeout must be positive")
	}

	transcriptionCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	transcript, err := s.transcriber.TranscribeClip(
		transcriptionCtx,
		audio,
		format,
	)
	if err != nil {
		if transcriptionCtx.Err() != nil {
			return "", transcriptionCtx.Err()
		}
		return "", fmt.Errorf("transcribe candidate audio: %w", err)
	}
	if err := transcriptionCtx.Err(); err != nil {
		return "", err
	}
	return strings.TrimSpace(transcript), nil
}

func clearBytes(audio []byte) {
	for index := range audio {
		audio[index] = 0
	}
}
