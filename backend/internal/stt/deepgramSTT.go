package stt

import (
	"context"
	"errors"
	"fmt"
	"sync"

	interfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/client/interfaces"
	client "github.com/deepgram/deepgram-go-sdk/v3/pkg/client/listen"
)

var initDeepgram sync.Once

type deepGramTranscriber struct {
	deepgramKey string
	completed   chan string
}

func NewDeepGramTranscriber(apiKey string) *deepGramTranscriber {
	return &deepGramTranscriber{
		deepgramKey: apiKey,
		completed:   make(chan string, 10),
	}
}

// CompletedUtterances returns transcripts after Deepgram marks the end of speech.
func (dg *deepGramTranscriber) CompletedUtterances() <-chan string {
	return dg.completed
}

func (dg *deepGramTranscriber) Transcribe(ctx context.Context, audio <-chan []byte) error {
	if dg.deepgramKey == "" {
		return errors.New("deepgram API key is required")
	}
	if audio == nil {
		return errors.New("audio channel is required")
	}

	initDeepgram.Do(client.InitWithDefault)

	clientOptions := &interfaces.ClientOptions{
		EnableKeepAlive: true,
	}
	transcriptionOptions := &interfaces.LiveTranscriptionOptions{
		Model:          "nova-3",
		Language:       "en-US",
		Encoding:       "linear16",
		Channels:       1,
		SampleRate:     16000,
		Punctuate:      true,
		SmartFormat:    true,
		InterimResults: true,
	}

	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	handler := newDeepgramHandler(dg.completed)
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

		case chunk, ok := <-audio:
			if !ok {
				if err := dgClient.Finalize(); err != nil {
					return fmt.Errorf("finalize Deepgram stream: %w", err)
				}
				return nil
			}
			if len(chunk) == 0 {
				continue
			}

			_, err := dgClient.Write(chunk)
			if err != nil {
				return fmt.Errorf("write audio to Deepgram: %w", err)
			}
		}
	}
}
