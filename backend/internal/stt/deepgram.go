package stt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	interfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/client/interfaces"
	client "github.com/deepgram/deepgram-go-sdk/v3/pkg/client/listen"
)

var initDeepgram sync.Once

const finalizeTimeout = 3 * time.Second
const speechEndpointSilence = "800"
const utteranceEndSilence = "1200"

type deepgramTranscriber struct {
	deepgramKey string
}

func NewDeepgramTranscriber(apiKey string) (*deepgramTranscriber, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errors.New("deepgram API key is required")
	}

	return &deepgramTranscriber{deepgramKey: apiKey}, nil
}

func (dg *deepgramTranscriber) Transcribe(
	ctx context.Context,
	audio <-chan AudioInput,
	completed chan<- Utterance,
	observe TranscriptObserver,
) error {
	return dg.transcribe(ctx, audio, completed, observe, false)
}

func (dg *deepgramTranscriber) TranscribeConversation(ctx context.Context, audio <-chan AudioInput, completed chan<- Utterance, observe TranscriptObserver) error {
	return dg.transcribe(ctx, audio, completed, observe, true)
}

func (dg *deepgramTranscriber) transcribe(ctx context.Context, audio <-chan AudioInput, completed chan<- Utterance, observe TranscriptObserver, persistent bool) error {
	if dg.deepgramKey == "" {
		return errors.New("deepgram API key is required")
	}
	if audio == nil {
		return errors.New("audio channel is required")
	}
	if completed == nil {
		return errors.New("completed utterance channel is required")
	}
	if observe == nil {
		return errors.New("transcript observer is required")
	}

	initDeepgram.Do(client.InitWithDefault)

	clientOptions := &interfaces.ClientOptions{
		EnableKeepAlive: true,
	}
	transcriptionOptions := liveTranscriptionOptions()

	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	handler := newDeepgramHandler(streamCtx, completed, observe, persistent)
	dgClient, err := client.NewWSUsingCallbackWithCancel(
		streamCtx,
		cancel,
		dg.deepgramKey,
		clientOptions,
		transcriptionOptions,
		handler,
	)
	if err != nil {
		return fmt.Errorf("create Deepgram client: %w", err)
	}

	if connected := dgClient.Connect(); !connected {
		return errors.New("connect to Deepgram")
	}
	defer dgClient.Stop()

	for {
		select {
		case <-streamCtx.Done():
			if errors.Is(streamCtx.Err(), context.Canceled) {
				return nil
			}
			return streamCtx.Err()

		case <-handler.Endpointed():
			return finalizeDeepgramStream(streamCtx, dgClient, handler)

		case input, ok := <-audio:
			if !ok {
				if persistent {
					return nil
				}
				return finalizeDeepgramStream(streamCtx, dgClient, handler)
			}
			if persistent && input.Finalize {
				if err := dgClient.Finalize(); err != nil {
					return fmt.Errorf("finalize conversation utterance: %w", err)
				}
				continue
			}
			chunk := input.PCM
			if len(chunk) == 0 {
				continue
			}

			handler.recordAudio(input)
			_, err := dgClient.Write(chunk)
			if persistent {
				clear(chunk)
			}
			if err != nil {
				return fmt.Errorf("write audio to Deepgram: %w", err)
			}
		}
	}
}

func liveTranscriptionOptions() *interfaces.LiveTranscriptionOptions {
	return &interfaces.LiveTranscriptionOptions{
		Model:          "nova-3",
		Language:       "en-US",
		Encoding:       "linear16",
		Channels:       1,
		SampleRate:     16000,
		Punctuate:      true,
		SmartFormat:    true,
		InterimResults: true,
		Endpointing:    speechEndpointSilence,
		// Endpointing relies on acoustic silence and can remain open in ambient
		// noise. UtteranceEnd uses finalized word gaps as the second end-of-turn
		// signal, which is better suited to a wearable microphone.
		UtteranceEndMs: utteranceEndSilence,
	}
}

type deepgramFinalizer interface {
	Finalize() error
}

func finalizeDeepgramStream(
	ctx context.Context,
	client deepgramFinalizer,
	handler *deepgramHandler,
) error {
	if err := client.Finalize(); err != nil {
		return fmt.Errorf("finalize Deepgram stream: %w", err)
	}

	select {
	case <-handler.Finalized():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(finalizeTimeout):
		return errors.New("timed out waiting for Deepgram finalization")
	}
}
