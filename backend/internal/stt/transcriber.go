package stt

import "context"

type TranscriptObserver func(text string) error

// ConversationTranscriber keeps one connection across utterance endpoints.
// A nil audio frame requests finalization of the current utterance only.
type ConversationTranscriber interface {
	TranscribeConversation(context.Context, <-chan []byte, chan<- string, TranscriptObserver) error
}

type Transcriber interface {
	// Transcribe streams audio, reports the latest partial transcript to
	// observe, and sends finalized utterances to completed. The caller owns the
	// completed channel and must keep consuming it until Transcribe returns.
	Transcribe(
		ctx context.Context,
		audio <-chan []byte,
		completed chan<- string,
		observe TranscriptObserver,
	) error
}
