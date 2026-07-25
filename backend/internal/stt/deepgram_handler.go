package stt

import (
	"context"
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
	ctx        context.Context
	completed  chan<- string
	finalized  chan struct{}
	finalize   sync.Once
}

func newDeepgramHandler(ctx context.Context, completed chan<- string) *deepgramHandler {
	return &deepgramHandler{
		DefaultCallbackHandler: websocket.NewDefaultCallbackHandler(),
		ctx:                    ctx,
		completed:              completed,
		finalized:              make(chan struct{}),
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

	var utterance string
	h.mu.Lock()
	if text != "" {
		if h.transcript != "" {
			h.transcript += " "
		}
		h.transcript += text
	}

	if (message.SpeechFinal || message.FromFinalize) && h.transcript != "" {
		utterance = h.transcript
		h.transcript = ""
	}
	h.mu.Unlock()

	if utterance != "" {
		select {
		case h.completed <- utterance:
		case <-h.ctx.Done():
		}
	}

	if message.FromFinalize {
		h.finalize.Do(func() { close(h.finalized) })
	}

	return nil
}

func (h *deepgramHandler) Finalized() <-chan struct{} {
	return h.finalized
}

func (h *deepgramHandler) Transcript() string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return h.transcript
}
