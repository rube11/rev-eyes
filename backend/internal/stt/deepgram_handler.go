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
	lastUpdate string
	ctx        context.Context
	completed  chan<- string
	observe    TranscriptObserver
	finalized  chan struct{}
	finalize   sync.Once
}

func newDeepgramHandler(
	ctx context.Context,
	completed chan<- string,
	observe TranscriptObserver,
) *deepgramHandler {
	return &deepgramHandler{
		DefaultCallbackHandler: websocket.NewDefaultCallbackHandler(),
		ctx:                    ctx,
		completed:              completed,
		observe:                observe,
		finalized:              make(chan struct{}),
	}
}

func (h *deepgramHandler) Message(message *msginterfaces.MessageResponse) error {
	if message == nil {
		return nil
	}

	var text string
	if len(message.Channel.Alternatives) > 0 {
		text = strings.TrimSpace(message.Channel.Alternatives[0].Transcript)
	}

	var update string
	var utterance string
	h.mu.Lock()
	if message.IsFinal && text != "" {
		if h.transcript != "" {
			h.transcript += " "
		}
		h.transcript += text
	}
	if message.IsFinal {
		update = h.transcript
	} else if text != "" {
		update = strings.TrimSpace(h.transcript + " " + text)
	}

	// SpeechFinal marks a natural pause in Deepgram's endpointing. The user may
	// continue speaking after it, so only the explicit stream finalization that
	// follows tap-to-finish may publish and clear the complete utterance.
	if message.FromFinalize && h.transcript != "" {
		utterance = h.transcript
		h.transcript = ""
	}
	if update == h.lastUpdate {
		update = ""
	} else if update != "" {
		h.lastUpdate = update
	}
	if utterance != "" {
		h.lastUpdate = ""
	}
	h.mu.Unlock()

	if update != "" {
		if err := h.observe(update); err != nil {
			return err
		}
	}
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
