package realtime

import (
	"context"
	"github.com/gorilla/websocket"
	"github.com/rube11/rev-eyes/backend/internal/ambient"
	"github.com/rube11/rev-eyes/backend/internal/candidate"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/tool"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAmbientProtocolKeepsManualStopInLocalListener(t *testing.T) {
	controlsSeen := make(chan string, 2)
	listenerStopped := make(chan struct{})
	s := NewServer(transcriberFunc(func(context.Context, <-chan stt.AudioInput, chan<- stt.Utterance, stt.TranscriptObserver) error {
		t.Error("paid streaming invoked")
		return nil
	}), Handlers{
		Authenticate: func(string) (tool.Scope, error) { return tool.Scope{UserID: "u", SessionID: "s"}, nil },
		Ambient: func(ctx context.Context, input <-chan ambient.Input, emit func(ambient.Clip)) error {
			defer close(listenerStopped)
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case event := <-input:
					if event.Control != "" {
						controlsSeen <- event.Control
					} else {
						emit(ambient.Clip{Audio: event.PCM, Start: 0, End: int64(len(event.PCM) / 2), Reason: candidate.WakeAssistantRequest})
					}
				}
			}
		},
		CandidateAudio: func(context.Context, []byte, stt.AudioFormat) (string, error) { return "glasses hello", nil },
		Utterance: func(context.Context, tool.Scope, string) (UtteranceResult, error) {
			return UtteranceResult{Text: "Hello"}, nil
		},
	})
	server := httptest.NewServer(s)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?ticket=t", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, command := range []string{"ambient_start", "listening_start", "listening_stop"} {
		if err = conn.WriteJSON(map[string]string{"type": command}); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{"listening_start", "listening_stop"} {
		select {
		case got := <-controlsSeen:
			if got != want {
				t.Fatalf("control=%s", got)
			}
		case <-time.After(time.Second):
			t.Fatal("control not delivered")
		}
	}
	if err = conn.WriteMessage(websocket.BinaryMessage, []byte{1, 0, 2, 0}); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var reply serverMessage
	if err = conn.ReadJSON(&reply); err != nil {
		t.Fatal(err)
	}
	if reply.Type != assistantResponseMessageType || reply.Text != "Hello" {
		t.Fatalf("reply=%+v", reply)
	}
	conn.WriteJSON(map[string]string{"type": "ambient_stop"})
	if err = conn.ReadJSON(&reply); err != nil {
		t.Fatal(err)
	}
	if reply.Type != listeningStoppedMessageType {
		t.Fatalf("stop reply=%+v", reply)
	}
	select {
	case <-listenerStopped:
	case <-time.After(time.Second):
		t.Fatal("listener leaked")
	}
}
func TestUnavailableAmbientDoesNotFallBackToPaidStreaming(t *testing.T) {
	paid := make(chan struct{}, 1)
	s := NewServer(transcriberFunc(func(context.Context, <-chan stt.AudioInput, chan<- stt.Utterance, stt.TranscriptObserver) error {
		paid <- struct{}{}
		return nil
	}), Handlers{Authenticate: func(string) (tool.Scope, error) { return tool.Scope{UserID: "u", SessionID: "s"}, nil }})
	server := httptest.NewServer(s)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?ticket=t", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"ambient_start", "listening_start", "ambient_start"} {
		conn.WriteJSON(map[string]string{"type": command})
	}
	conn.SetReadDeadline(time.Now().Add(time.Second))
	for i := 0; i < 2; i++ {
		var message serverMessage
		if err = conn.ReadJSON(&message); err != nil {
			t.Fatal(err)
		}
		if message.Error == "" {
			t.Fatal("missing unavailable error")
		}
	}
	conn.Close()
	select {
	case <-paid:
		t.Fatal("unexpected paid streaming fallback")
	default:
	}
}
