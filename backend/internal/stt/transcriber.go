package stt

import "context"

type TranscriptObserver func(text string) error

// AudioInput preserves the order of PCM and utterance finalization commands.
// The producer transfers ownership of PCM to the transcription worker.
type AudioInput struct {
	PCM      []byte
	Finalize bool
}

// ConversationTranscriber keeps one connection across utterance endpoints.
type ConversationTranscriber interface {
	TranscribeConversation(context.Context, <-chan AudioInput, chan<- string, TranscriptObserver) error
}

type Transcriber interface {
	// Transcribe streams audio, reports the latest partial transcript to
	// observe, and sends finalized utterances to completed. The caller owns the
	// completed channel and must keep consuming it until Transcribe returns.
	Transcribe(
		ctx context.Context,
		audio <-chan AudioInput,
		completed chan<- string,
		observe TranscriptObserver,
	) error
}
