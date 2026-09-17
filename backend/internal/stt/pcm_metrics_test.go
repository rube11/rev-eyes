package stt

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"
	"time"
)

func TestMeasureLinear16PCM(t *testing.T) {
	audio := encodeLinear16(0, 3_000, -4_000, 0)
	metrics, err := MeasureLinear16PCM(audio, AudioFormat{
		Encoding:   EncodingLinear16,
		SampleRate: 4,
		Channels:   1,
	})
	if err != nil {
		t.Fatalf("MeasureLinear16PCM() error = %v", err)
	}
	if metrics.Duration != time.Second {
		t.Errorf("Duration = %s, want 1s", metrics.Duration)
	}
	if metrics.PeakAmplitude != 4_000 {
		t.Errorf("PeakAmplitude = %d, want 4000", metrics.PeakAmplitude)
	}
	if math.Abs(metrics.RMSAmplitude-2_500) > 0.001 {
		t.Errorf("RMSAmplitude = %f, want 2500", metrics.RMSAmplitude)
	}
}

func TestMeasureLinear16PCMDurationUsesChannelFrames(t *testing.T) {
	metrics, err := MeasureLinear16PCM(
		encodeLinear16(1_000, -1_000, 1_000, -1_000),
		AudioFormat{
			Encoding:   EncodingLinear16,
			SampleRate: 2,
			Channels:   2,
		},
	)
	if err != nil {
		t.Fatalf("MeasureLinear16PCM() error = %v", err)
	}
	if metrics.Duration != time.Second {
		t.Errorf("Duration = %s, want 1s", metrics.Duration)
	}
}

func TestMeasureLinear16PCMHandlesMinimumSample(t *testing.T) {
	metrics, err := MeasureLinear16PCM(
		encodeLinear16(-32_768),
		AudioFormat{Encoding: EncodingLinear16, SampleRate: 1, Channels: 1},
	)
	if err != nil {
		t.Fatalf("MeasureLinear16PCM() error = %v", err)
	}
	if metrics.PeakAmplitude != 32_768 {
		t.Errorf("PeakAmplitude = %d, want 32768", metrics.PeakAmplitude)
	}
	if metrics.RMSAmplitude != 32_768 {
		t.Errorf("RMSAmplitude = %f, want 32768", metrics.RMSAmplitude)
	}
}

func TestMeasureLinear16PCMRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name   string
		audio  []byte
		format AudioFormat
		want   error
	}{
		{
			name:   "empty clip",
			audio:  nil,
			format: AudioFormat{Encoding: EncodingLinear16, SampleRate: 16_000, Channels: 1},
			want:   ErrAudioClipInvalid,
		},
		{
			name:   "unaligned frame",
			audio:  []byte{1, 2},
			format: AudioFormat{Encoding: EncodingLinear16, SampleRate: 16_000, Channels: 2},
			want:   ErrAudioClipInvalid,
		},
		{
			name:   "unsupported encoding",
			audio:  []byte{1, 2},
			format: AudioFormat{Encoding: "mulaw", SampleRate: 8_000, Channels: 1},
			want:   ErrAudioFormatInvalid,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := MeasureLinear16PCM(test.audio, test.format)
			if !errors.Is(err, test.want) {
				t.Fatalf("MeasureLinear16PCM() error = %v, want %v", err, test.want)
			}
		})
	}
}

func encodeLinear16(samples ...int16) []byte {
	audio := make([]byte, len(samples)*2)
	for index, sample := range samples {
		binary.LittleEndian.PutUint16(audio[index*2:], uint16(sample))
	}
	return audio
}
