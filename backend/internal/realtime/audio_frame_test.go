package realtime

import (
	"bytes"
	"github.com/rube11/rev-eyes/backend/internal/speech"
	"testing"
)

func TestSpeakerPCMProtocol(t *testing.T) {
	for _, role := range []speech.Role{speech.Unknown, speech.Self, speech.Other} {
		pcm, got, err := decodeAudioFrame([]byte{1, byte(role), 42, 0}, speakerPCMEncoding)
		if err != nil || got != role || !bytes.Equal(pcm, []byte{42, 0}) {
			t.Fatalf("%v %v %v", pcm, got, err)
		}
	}
	for _, frame := range [][]byte{{}, {1}, {1, 0}, {2, 0, 1, 0}, {1, 3, 1, 0}, {1, 1, 0}} {
		if _, _, err := decodeAudioFrame(frame, speakerPCMEncoding); err == nil {
			t.Fatalf("accepted malformed frame %v", frame)
		}
	}
	// Raw PCM beginning with our header bytes is not mistaken for a header.
	frame := []byte{1, 1, 42, 0}
	pcm, role, err := decodeAudioFrame(frame, "")
	if err != nil || role != speech.Unknown || !bytes.Equal(pcm, frame) {
		t.Fatal("legacy PCM changed")
	}
}
