package stt

import "context"

type Transcriber interface {
	Transcribe(ctx context.Context, audio <-chan []byte) error
}
