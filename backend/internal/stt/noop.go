package stt

import "context"

type NoopTranscriber struct{}

func (n *NoopTranscriber) Transcribe(ctx context.Context, audio []byte) (string, error) {
	return "", nil
}
