package stt

import (
	"encoding/binary"
	"errors"
	"math"
	"time"
)

// ErrAudioClipInvalid indicates that PCM bytes are empty or not frame-aligned.
var ErrAudioClipInvalid = errors.New("audio clip is invalid")

// PCMSignalMetrics summarizes a linear16 clip without retaining its contents.
type PCMSignalMetrics struct {
	Duration      time.Duration
	PeakAmplitude int32
	RMSAmplitude  float64
}

// MeasureLinear16PCM calculates non-content signal metrics for headerless PCM.
func MeasureLinear16PCM(audio []byte, format AudioFormat) (PCMSignalMetrics, error) {
	if err := format.Validate(); err != nil {
		return PCMSignalMetrics{}, err
	}
	if format.Encoding != EncodingLinear16 {
		return PCMSignalMetrics{}, ErrAudioFormatInvalid
	}
	bytesPerFrame := format.Channels * 2
	if len(audio) == 0 || len(audio)%bytesPerFrame != 0 {
		return PCMSignalMetrics{}, ErrAudioClipInvalid
	}

	var peak int32
	var sumSquares float64
	sampleCount := len(audio) / 2
	for offset := 0; offset < len(audio); offset += 2 {
		sample := int32(int16(binary.LittleEndian.Uint16(audio[offset : offset+2])))
		magnitude := sample
		if magnitude < 0 {
			magnitude = -magnitude
		}
		if magnitude > peak {
			peak = magnitude
		}
		sumSquares += float64(sample) * float64(sample)
	}

	frameCount := len(audio) / bytesPerFrame
	return PCMSignalMetrics{
		Duration:      time.Duration(frameCount) * time.Second / time.Duration(format.SampleRate),
		PeakAmplitude: peak,
		RMSAmplitude:  math.Sqrt(sumSquares / float64(sampleCount)),
	}, nil
}
