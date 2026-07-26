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
	audio <-chan []byte,
	completed chan<- string,
	observe TranscriptObserver,
) error {
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

	handler := newDeepgramHandler(streamCtx, completed, observe)
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

				select {
				case <-handler.Finalized():
					return nil
				case <-streamCtx.Done():
					return streamCtx.Err()
				case <-time.After(finalizeTimeout):
					return errors.New("timed out waiting for Deepgram finalization")
				}
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
