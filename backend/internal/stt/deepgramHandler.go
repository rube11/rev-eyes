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
}

func newDeepgramHandler() *deepgramHandler {
	return &deepgramHandler{
		DefaultCallbackHandler: websocket.NewDefaultCallbackHandler(),
	}
}

func (h *deepgramHandler) Message(message *msginterfaces.MessageResponse) error {
	if message == nil || !message.IsFinal || len(message.Channel.Alternatives) == 0 {
		return nil
	}

	text := strings.TrimSpace(message.Channel.Alternatives[0].Transcript)
	if text == "" {
		return nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.transcript != "" {
		h.transcript += " "
	}
	h.transcript += text

	return nil
}

func (h *deepgramHandler) Transcript() string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return h.transcript
}
