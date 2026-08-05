package stt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	api "github.com/deepgram/deepgram-go-sdk/v3/pkg/api/listen/v1/rest"
	apiinterfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/api/listen/v1/rest/interfaces"
	interfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/client/interfaces"
	client "github.com/deepgram/deepgram-go-sdk/v3/pkg/client/listen"
)

func (dg *deepgramTranscriber) TranscribeClip(
	ctx context.Context,
	audio []byte,
	format AudioFormat,
) (string, error) {
	if ctx == nil {
		return "", errors.New("transcription context is required")
	}
	if dg == nil || strings.TrimSpace(dg.deepgramKey) == "" {
		return "", errors.New("deepgram API key is required")
	}
	if len(audio) == 0 {
		return "", errors.New("audio clip is required")
	}
	if err := format.Validate(); err != nil {
		return "", err
	}
	if format.Encoding == EncodingLinear16 && len(audio)%(format.Channels*2) != 0 {
		return "", errors.New("linear16 audio is not sample aligned")
	}

	initDeepgram.Do(client.InitWithDefault)
	restClient := client.NewREST(dg.deepgramKey, &interfaces.ClientOptions{})
	if restClient == nil {
		return "", errors.New("create Deepgram prerecorded client")
	}
	response, err := api.New(restClient).FromStream(
		ctx,
		bytes.NewReader(audio),
		&interfaces.PreRecordedTranscriptionOptions{
			Model:       "nova-3",
			Language:    "en-US",
			Encoding:    format.Encoding,
			Channels:    format.Channels,
			SampleRate:  format.SampleRate,
			Punctuate:   true,
			SmartFormat: true,
		},
	)
	if err != nil {
		return "", fmt.Errorf("transcribe Deepgram clip: %w", err)
	}
	return prerecordedTranscript(response), nil
}

func prerecordedTranscript(response *apiinterfaces.PreRecordedResponse) string {
	if response == nil || response.Results == nil {
		return ""
	}
	var transcripts []string
	for _, channel := range response.Results.Channels {
		for _, alternative := range channel.Alternatives {
			transcript := strings.TrimSpace(alternative.Transcript)
			if transcript != "" {
				transcripts = append(transcripts, transcript)
				break
			}
		}
	}
	if len(transcripts) == 0 {
		for _, utterance := range response.Results.Utterances {
			if transcript := strings.TrimSpace(utterance.Transcript); transcript != "" {
				transcripts = append(transcripts, transcript)
			}
		}
	}
	return strings.TrimSpace(strings.Join(transcripts, " "))
}
