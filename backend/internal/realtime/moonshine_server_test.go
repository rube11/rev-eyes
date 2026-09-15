package realtime

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

// This worker substitutes only model inference. The socket, raw PCM framing,
// server-side wake policy, clip handoff, and transcript delivery are real.
func TestMoonshineStreamsAllAudioAndGatesDeepgramOnServer(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	worker := filepath.Join(t.TempDir(), "worker.py")
	source := `import sys, struct, json, base64
print(json.dumps({"type":"ready"}), flush=True)
index=0
while True:
    header=sys.stdin.buffer.read(4)
    if not header: break
    size=struct.unpack('<I',header)[0]
    audio=sys.stdin.buffer.read(size)
    index+=1
    text='The weather is nice' if index==1 else 'Glasses remind me to buy milk'
    print(json.dumps({'type':'moonshine_transcript','id':str(index),'text':text,'final':True,'pcm':base64.b64encode(audio).decode()}),flush=True)
print(json.dumps({'type':'stopped'}),flush=True)
`
	if err := os.WriteFile(worker, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	clips := make(chan []byte, 2)
	server := NewMoonshineServer(python, worker, func(string) (tool.Scope, error) { return tool.Scope{}, nil }, nil,
		func(_ context.Context, audio []byte, format stt.AudioFormat) (string, error) {
			if format.SampleRate != 16000 || format.Channels != 1 {
				t.Errorf("unexpected format: %+v", format)
			}
			clips <- append([]byte(nil), audio...)
			return "Glasses, remind me to buy milk.", nil
		})
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	conn, _, err := websocket.DefaultDialer.Dial(websocketTestURL(httpServer.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var event moonshineEvent
	if err = conn.ReadJSON(&event); err != nil || event.Type != "ready" {
		t.Fatalf("ready: %+v %v", event, err)
	}
	for _, audio := range [][]byte{{1, 0, 2, 0}, {3, 0, 4, 0}} {
		if err = conn.WriteMessage(websocket.BinaryMessage, audio); err != nil {
			t.Fatal(err)
		}
	}
	conn.WriteJSON(map[string]string{"type": "listening_stop"})
	moonshineCount, deepgramCount := 0, 0
	for {
		if err = conn.ReadJSON(&event); err != nil {
			t.Fatal(err)
		}
		if len(event.PCM) > 0 {
			t.Fatal("raw audio leaked to client")
		}
		if event.Type == "moonshine_transcript" {
			moonshineCount++
		}
		if event.Type == "deepgram_transcript" {
			deepgramCount++
			if event.ID != "2" {
				t.Fatal("wrong clip transcribed")
			}
		}
		if event.Type == "stopped" {
			break
		}
	}
	if moonshineCount != 2 || deepgramCount != 1 {
		t.Fatalf("Moonshine=%d Deepgram=%d", moonshineCount, deepgramCount)
	}
	if len(clips) != 1 {
		t.Fatalf("Deepgram got %d clips", len(clips))
	}
	clip := <-clips
	if len(clip) != 4 || clip[0] != 3 {
		t.Fatalf("incorrect selected audio: %v", clip)
	}
}

// Opt-in deployment smoke test: real native Moonshine and real Deepgram.
// The input is a synthetic 16kHz mono PCM fixture containing a wake phrase.
func TestMoonshineNativeDeepgram(t *testing.T) {
	fixture := os.Getenv("MOONSHINE_TEST_AUDIO")
	if fixture == "" {
		t.Skip("set MOONSHINE_TEST_AUDIO for deployment smoke test")
	}
	pcm, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	accurate, err := stt.NewDeepgramTranscriber(os.Getenv("DEEPGRAM_API_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewMoonshineServer(os.Getenv("MOONSHINE_PYTHON"), os.Getenv("MOONSHINE_WORKER"),
		func(string) (tool.Scope, error) { return tool.Scope{}, nil }, nil, accurate.TranscribeClip)
	httpServer := httptest.NewServer(s)
	defer httpServer.Close()
	conn, _, err := websocket.DefaultDialer.Dial(websocketTestURL(httpServer.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	var event moonshineEvent
	if err = conn.ReadJSON(&event); err != nil || event.Type != "ready" {
		t.Fatalf("ready: %+v %v", event, err)
	}
	for offset := 0; offset < len(pcm); offset += 3200 {
		end := offset + 3200
		if end > len(pcm) {
			end = len(pcm)
		}
		if err = conn.WriteMessage(websocket.BinaryMessage, pcm[offset:end]); err != nil {
			t.Fatal(err)
		}
	}
	conn.WriteJSON(map[string]string{"type": "listening_stop"})
	moonshineSeen, deepgramSeen := false, false
	for {
		if err = conn.ReadJSON(&event); err != nil {
			t.Fatal(err)
		}
		if event.Error != "" {
			t.Fatal(event.Error)
		}
		if event.Type == "moonshine_transcript" && event.Text != "" {
			moonshineSeen = true
		}
		if event.Type == "deepgram_transcript" && event.Text != "" {
			deepgramSeen = true
			t.Log("Real Deepgram transcript received")
		}
		if event.Type == "stopped" {
			break
		}
	}
	if !moonshineSeen || !deepgramSeen {
		t.Fatalf("Moonshine=%t Deepgram=%t", moonshineSeen, deepgramSeen)
	}
}
