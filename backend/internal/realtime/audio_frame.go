package realtime

import (
	"errors"
	"github.com/rube11/rev-eyes/backend/internal/speech"
)

const speakerPCMEncoding = "pcm_speaker_v1"

func decodeAudioFrame(data []byte, encoding string) ([]byte, speech.Role, error) {
	role := speech.Unknown
	if encoding != speakerPCMEncoding {
		// Legacy clients send raw PCM; their existing consumer validates it.
		return data, role, nil
	}
	if encoding == speakerPCMEncoding {
		if len(data) < 4 || data[0] != 1 || data[1] > byte(speech.Other) {
			return nil, role, errors.New("invalid speaker PCM frame")
		}
		// Older packages still send role headers. Strip the framing, but ignore
		// identification on the server too so an installed old beta cannot
		// reintroduce self/other labels or speaker-based behavior.
		data = data[2:]
	}
	if len(data) == 0 || len(data)%2 != 0 {
		return nil, speech.Unknown, errors.New("PCM frame is not sample aligned")
	}
	return data, role, nil
}
