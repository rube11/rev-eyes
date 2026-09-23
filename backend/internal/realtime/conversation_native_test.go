//go:build moonshine && cgo

package realtime

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rube11/rev-eyes/backend/internal/ambient"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/stt/moonshine"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type countedLiveConversation struct {
	stt.Transcriber
	live  stt.ConversationTranscriber
	calls atomic.Int32
}

func (c *countedLiveConversation) TranscribeConversation(ctx context.Context, audio <-chan stt.AudioInput, completed chan<- stt.Utterance, observe stt.TranscriptObserver) error {
	c.calls.Add(1)
	return c.live.TranscribeConversation(ctx, audio, completed, observe)
}

func TestNativeStreamingConversationRoundTrip(t *testing.T) {
	if os.Getenv("MOONSHINE_TEST_LIVE_DEEPGRAM") != "true" {
		t.Skip("opt-in real native/Deepgram conversation test; also set MOONSHINE_TEST_FOLLOWUP_PCM")
	}
	first, err := os.ReadFile(os.Getenv("MOONSHINE_TEST_PCM"))
	if err != nil {
		t.Fatal(err)
	}
	followup, err := os.ReadFile(os.Getenv("MOONSHINE_TEST_FOLLOWUP_PCM"))
	if err != nil {
		t.Fatal(err)
	}
	factory, err := moonshine.New(os.Getenv("MOONSHINE_TEST_MODEL_DIR"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer factory.Close()
	deepgram, err := stt.NewDeepgramTranscriber(os.Getenv("DEEPGRAM_API_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	counted := &countedLiveConversation{Transcriber: deepgram, live: deepgram}
	listener := &ambient.Listener{Factory: factory}
	app := NewServer(counted, Handlers{ConversationTranscriber: counted,
		Ambient: listener.Run, AmbientStreaming: listener.RunStreaming,
		CandidateAudio: func(context.Context, []byte, stt.AudioFormat) (string, error) {
			t.Error("used prerecorded request")
			return "", nil
		},
		Authenticate: func(string) (tool.Scope, error) {
			return tool.Scope{UserID: "synthetic-stream", SessionID: "synthetic-stream"}, nil
		},
		Utterance: func(_ context.Context, _ tool.Scope, text string) (UtteranceResult, error) {
			t.Logf("Assistant turn: %s", text)
			return UtteranceResult{Text: "Received: " + text}, nil
		},
	})
	app.conversationIdle = 5 * time.Second
	server := httptest.NewServer(app)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	if err := conn.WriteJSON(map[string]string{"type": "ambient_start"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	firstResponse := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		send := func(pcm []byte) error {
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			for i := 0; i < len(pcm); i += 640 {
				select {
				case <-ticker.C:
				case <-ctx.Done():
					return ctx.Err()
				}
				if err := conn.WriteMessage(websocket.BinaryMessage, pcm[i:min(i+640, len(pcm))]); err != nil {
					return err
				}
			}
			return nil
		}
		if err := send(append(first, make([]byte, 2*32000)...)); err != nil {
			writerDone <- err
			return
		}
		select {
		case <-firstResponse:
		case <-ctx.Done():
			writerDone <- ctx.Err()
			return
		}
		writerDone <- send(append(followup, make([]byte, 6*32000)...))
	}()
	responses := 0
	for {
		var message serverMessage
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatal(err)
		}
		if message.Error != "" {
			t.Fatal(message.Error)
		}
		if message.Type == assistantResponseMessageType {
			responses++
			if responses == 1 {
				if !strings.Contains(strings.ToLower(message.Text), "milk") {
					t.Fatal(message.Text)
				}
				close(firstResponse)
			}
			if responses == 2 && !strings.Contains(strings.ToLower(message.Text), "tomorrow") {
				t.Fatal(message.Text)
			}
		}
		if message.Type == "conversation_idle" {
			break
		}
	}
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}
	if responses != 2 || counted.calls.Load() != 1 {
		t.Fatalf("responses=%d Deepgram connections=%d", responses, counted.calls.Load())
	}
	if err := conn.WriteJSON(map[string]string{"type": "ambient_stop"}); err != nil {
		t.Fatal(err)
	}
	for {
		var message serverMessage
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatal(err)
		}
		if message.Type == listeningStoppedMessageType {
			break
		}
	}
	t.Log("Keyword opened one Deepgram connection; two assistant turns completed; idle closed it and returned to Moonshine")
}
