package stt

import (
	"strings"
	"sync"

	websocket "github.com/deepgram/deepgram-go-sdk/v3/pkg/api/listen/v1/websocket"
	msginterfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/api/listen/v1/websocket/interfaces"
)

// deepgramHandler receives transcription events from Deepgram.
type deepgramHandler struct {
	*websocket.DefaultCallbackHandler

	mu         sync.RWMutex
	transcript string
	completed  chan<- string
}

func newDeepgramHandler(completed chan<- string) *deepgramHandler {
	return &deepgramHandler{
		DefaultCallbackHandler: websocket.NewDefaultCallbackHandler(),
		completed:              completed,
	}
}

func (h *deepgramHandler) Message(message *msginterfaces.MessageResponse) error {
	if message == nil {
		return nil
	}

	var text string
	if message.IsFinal && len(message.Channel.Alternatives) > 0 {
		text = strings.TrimSpace(message.Channel.Alternatives[0].Transcript)
	}

	h.mu.Lock()
	if text != "" {
		if h.transcript != "" {
			h.transcript += " "
		}
		h.transcript += text
	}

	if message.SpeechFinal && h.transcript != "" {
		utterance := h.transcript
		h.transcript = ""
		h.mu.Unlock()

		h.completed <- utterance
		return nil
	}
	h.mu.Unlock()

	return nil
}

func (h *deepgramHandler) Transcript() string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return h.transcript
}
