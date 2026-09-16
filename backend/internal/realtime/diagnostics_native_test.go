//go:build moonshine && cgo

package realtime

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rube11/rev-eyes/backend/internal/ambient"
	"github.com/rube11/rev-eyes/backend/internal/candidate"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/stt/moonshine"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

// This opt-in deployment smoke test uses synthetic speech, real native
// inference, and a paid Deepgram clip. Authentication is scoped to the local
// test server; it never creates a production session or assistant action.
func TestNativeDiagnosticsAudioRoundTrip(t *testing.T) {
	if os.Getenv("MOONSHINE_TEST_LIVE_DEEPGRAM") != "true" {
		t.Skip("set MOONSHINE_TEST_LIVE_DEEPGRAM=true, MOONSHINE_TEST_MODEL_DIR, and MOONSHINE_TEST_PCM (16kHz mono PCM16LE saying 'remind me to buy milk')")
	}
	pcm, err := os.ReadFile(os.Getenv("MOONSHINE_TEST_PCM"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) == 0 || len(pcm)%2 != 0 || len(pcm) > 20*32000 {
		t.Fatal("expected sample-aligned speech lasting at most 20 seconds")
	}
	// End with silence so both native endpointing and the clip post-roll finish.
	pcm = append(pcm, make([]byte, 4*32000)...)
	// Audio acknowledgments arrive once per full second. Pad to that boundary
	// so the test can require acknowledgment of every byte it sends.
	if remainder := len(pcm) % 32000; remainder != 0 {
		pcm = append(pcm, make([]byte, 32000-remainder)...)
	}
	factory, err := moonshine.New(os.Getenv("MOONSHINE_TEST_MODEL_DIR"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer factory.Close()
	transcriber, err := stt.NewDeepgramTranscriber(os.Getenv("DEEPGRAM_API_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	clips, err := candidate.NewService(transcriber)
	if err != nil {
		t.Fatal(err)
	}
	listener := &ambient.Listener{Factory: factory}
	app := NewServer(nil, Handlers{
		Ambient: listener.Run, CandidateAudio: clips.Process,
		Authenticate: func(string) (tool.Scope, error) {
			return tool.Scope{UserID: "synthetic-smoke", SessionID: "synthetic-smoke"}, nil
		},
		Utterance: func(context.Context, tool.Scope, string) (UtteranceResult, error) {
			t.Error("test speech reached assistant actions")
			return UtteranceResult{}, nil
		},
	})
	server := httptest.NewServer(app.DiagnosticsServer(listener.RunObserved))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws/moonshine", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	if err := conn.WriteJSON(map[string]string{"type": "ambient_start"}); err != nil {
		t.Fatal(err)
	}
	var message serverMessage
	if err := conn.ReadJSON(&message); err != nil || message.Type != "ready" {
		t.Fatalf("native ready: %+v %v", message, err)
	}
	started := time.Now()
	writerDone := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for offset := 0; offset < len(pcm); offset += 640 {
			<-ticker.C
			if err := conn.WriteMessage(websocket.BinaryMessage, pcm[offset:min(offset+640, len(pcm))]); err != nil {
				writerDone <- err
				return
			}
		}
		writerDone <- nil
	}()
	wantAck := int64(len(pcm) / 32000 * 32000)
	var acknowledged int64
	rough, accurate, keyword := false, false, false
	for acknowledged < wantAck || !rough || !accurate || !keyword {
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("audio round trip: received=%d/%d rough=%v accurate=%v keyword=%v: %v", acknowledged, wantAck, rough, accurate, keyword, err)
		}
		switch message.Type {
		case "audio_received":
			acknowledged = message.ReceivedBytes
		case "moonshine_transcript":
			if strings.Contains(strings.ToLower(message.Text), "milk") {
				if !rough {
					t.Logf("Moonshine: %s", message.Text)
				}
				rough = true
			}
		case "keyword_detected":
			keyword = true
		case "deepgram_transcript":
			t.Logf("Deepgram: %s", message.Text)
			accurate = strings.Contains(strings.ToLower(message.Text), "milk")
		case "error":
			t.Fatalf("server error: %+v", message)
		}
	}
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}
	t.Logf("Sent %d PCM bytes (%.2fs); server processed/acknowledged %d bytes in %s", len(pcm), float64(len(pcm))/32000, acknowledged, time.Since(started))
	if err := conn.WriteJSON(map[string]string{"type": "ambient_stop"}); err != nil {
		t.Fatal(err)
	}
	for message.Type != listeningStoppedMessageType {
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatal(err)
		}
	}
}
