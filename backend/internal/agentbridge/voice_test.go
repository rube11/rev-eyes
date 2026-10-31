package agentbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rube11/rev-eyes/backend/internal/realtime"
	"github.com/rube11/rev-eyes/backend/internal/speech"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type voiceProvider struct {
	*controlProvider
	calls chan [3]string
}

func (p voiceProvider) Send(_ context.Context, id, text, key string) (json.RawMessage, error) {
	p.calls <- [3]string{id, text, key}
	return json.RawMessage(`{"threadId":"mcp:launch:chosen","messageId":"m","runId":"r","delivery":"queued"}`), nil
}

type voiceTranscriber struct{ other bool }

func (v voiceTranscriber) Transcribe(ctx context.Context, audio <-chan stt.AudioInput, completed chan<- stt.Utterance, observe stt.TranscriptObserver) error {
	select {
	case <-audio:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := observe("repeat"); err != nil {
		return err
	}
	utterance := stt.Utterance{Text: "repeat"}
	if v.other {
		utterance.Segments = []speech.Segment{{Role: speech.Other, Text: "repeat"}}
	}
	// The second endpoint must not become a second command from the same tap.
	for range 2 {
		select {
		case completed <- utterance:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestVoiceIsBoundToOwnerAndTargetAndSendsOncePerTap(t *testing.T) {
	for _, other := range []bool{false, true} {
		t.Run(map[bool]string{false: "intentional speech", true: "context-only blocked"}[other], func(t *testing.T) {
			calls := make(chan [3]string, 5)
			provider := voiceProvider{newControlProvider(), calls}
			gateway := testGateway(t, provider)
			handler := gateway.VoiceHandler(voiceTranscriber{other: other}, realtime.NewHub(), func(ticket string) (tool.Scope, error) {
				return tool.Scope{UserID: ticket, SessionID: "voice-session"}, nil
			}, func(*http.Request) bool { return true })
			server := httptest.NewServer(handler)
			defer server.Close()
			url := "ws" + strings.TrimPrefix(server.URL, "http") + "?connection_id=laptop&thread_id=mcp:launch:chosen&ticket="
			if socket, response, err := websocket.DefaultDialer.Dial(url+"bob", nil); err == nil {
				socket.Close()
				t.Fatal("cross-owner socket accepted")
			} else if response.StatusCode != 401 {
				t.Fatalf("wrong rejection %d", response.StatusCode)
			}
			socket, _, err := websocket.DefaultDialer.Dial(url+"alice", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer socket.Close()
			_ = socket.SetReadDeadline(time.Now().Add(3 * time.Second))
			if err = socket.WriteJSON(map[string]string{"type": "listening_start", "thread_id": "forged"}); err != nil {
				t.Fatal(err)
			}
			if err = socket.WriteMessage(websocket.BinaryMessage, []byte{1, 0, 2, 0}); err != nil {
				t.Fatal(err)
			}
			responseText := ""
			for range 6 {
				var message struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				if err = socket.ReadJSON(&message); err != nil {
					t.Fatal(err)
				}
				if message.Type == "assistant_response" {
					responseText = message.Text
				}
				if message.Type == "listening_stopped" {
					break
				}
			}
			if responseText == "" {
				t.Fatal("missing delivery feedback")
			}
			if other {
				if len(calls) != 0 || !strings.Contains(responseText, "not sent") {
					t.Fatalf("context speech dispatched: %q", responseText)
				}
			} else {
				select {
				case call := <-calls:
					if call[0] != "mcp:launch:chosen" || call[1] != "repeat" || !strings.HasPrefix(call[2], "eyes-") {
						t.Fatalf("wrong dispatch %v", call)
					}
				default:
					t.Fatal("no message sent")
				}
				if len(calls) != 0 {
					t.Fatal("multiple sends for one tap")
				}
				if !strings.Contains(responseText, "queued") {
					t.Fatal("misleading receipt")
				}
			}
		})
	}
}
