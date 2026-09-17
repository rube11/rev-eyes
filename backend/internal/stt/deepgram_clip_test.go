package stt

import (
	"context"
	"errors"
	"testing"

	apiinterfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/api/listen/v1/rest/interfaces"
)

func TestPrerecordedTranscriptUsesFirstNonEmptyAlternativePerChannel(t *testing.T) {
	response := &apiinterfaces.PreRecordedResponse{
		Results: &apiinterfaces.Result{
			Channels: []apiinterfaces.Channel{
				{Alternatives: []apiinterfaces.Alternative{
					{Transcript: "  I need to go to the gym "},
					{Transcript: "ignored alternative"},
				}},
				{Alternatives: []apiinterfaces.Alternative{
					{Transcript: " tomorrow after class. "},
				}},
			},
		},
	}

	got := prerecordedTranscript(response)
	if got != "I need to go to the gym tomorrow after class." {
		t.Fatalf("prerecordedTranscript() = %q", got)
	}
}

func TestPrerecordedTranscriptFallsBackToUtterances(t *testing.T) {
	response := &apiinterfaces.PreRecordedResponse{
		Results: &apiinterfaces.Result{
			Utterances: []apiinterfaces.Utterance{
				{Transcript: "remind me"},
				{Transcript: "tomorrow"},
			},
		},
	}

	if got := prerecordedTranscript(response); got != "remind me tomorrow" {
		t.Fatalf("prerecordedTranscript() = %q", got)
	}
}

func TestDeepgramClipRejectsInvalidInputBeforeRequest(t *testing.T) {
	transcriber := &deepgramTranscriber{deepgramKey: "test-key"}
	validFormat := AudioFormat{
		Encoding:   EncodingLinear16,
		SampleRate: 16_000,
		Channels:   1,
	}

	if _, err := transcriber.TranscribeClip(context.Background(), nil, validFormat); err == nil {
		t.Fatal("TranscribeClip() accepted empty audio")
	}
	if _, err := transcriber.TranscribeClip(
		context.Background(),
		[]byte{1},
		validFormat,
	); err == nil {
		t.Fatal("TranscribeClip() accepted an unaligned linear16 sample")
	}
	if _, err := transcriber.TranscribeClip(
		context.Background(),
		[]byte{1, 2},
		AudioFormat{},
	); !errors.Is(err, ErrAudioFormatInvalid) {
		t.Fatalf("TranscribeClip() error = %v, want ErrAudioFormatInvalid", err)
	}
}
