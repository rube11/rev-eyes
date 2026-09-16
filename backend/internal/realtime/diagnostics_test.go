package realtime

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rube11/rev-eyes/backend/internal/ambient"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type diagnosticFactory struct{}

type pausedFactory struct{ release <-chan struct{} }

func (f pausedFactory) Open() (ambient.Stream, error) { return &pausedStream{release: f.release}, nil }

type pausedStream struct{ release <-chan struct{} }

func (s *pausedStream) Add([]float32) error               { <-s.release; return nil }
func (*pausedStream) Transcript() ([]ambient.Line, error) { return nil, nil }
func (*pausedStream) Reset() error                        { return nil }
func (*pausedStream) Close()                              {}

func TestDiagnosticsPreservesSmallFramesDuringInferencePause(t *testing.T) {
	release := make(chan struct{})
	// Model inference can pause longer than the 16-frame queue represents.
	// Release it even if the client fails, so server cleanup cannot hang.
	timer := time.AfterFunc(200*time.Millisecond, func() { close(release) })
	defer func() {
		if timer.Stop() {
			close(release)
		}
	}()
	listener := &ambient.Listener{Factory: pausedFactory{release: release}}
	app := NewServer(nil, Handlers{
		Ambient:        listener.Run,
		Authenticate:   func(string) (tool.Scope, error) { return tool.Scope{UserID: "u", SessionID: "s"}, nil },
		CandidateAudio: func(context.Context, []byte, stt.AudioFormat) (string, error) { return "", nil },
	})
	server := httptest.NewServer(app.DiagnosticsServer(listener.RunObserved))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws/moonshine", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := conn.WriteJSON(map[string]string{"type": "ambient_start"}); err != nil {
		t.Fatal(err)
	}
	var message serverMessage
	if err := conn.ReadJSON(&message); err != nil || message.Type != "ready" {
		t.Fatalf("ready: %+v %v", message, err)
	}
	// Two seconds of PCM in the same 20ms frame size used by live audio.
	for i := 0; i < 100; i++ {
		if err := conn.WriteMessage(websocket.BinaryMessage, make([]byte, 640)); err != nil {
			t.Fatal(err)
		}
	}
	for message.ReceivedBytes < 64000 {
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("lost audio during inference pause: %v", err)
		}
	}
	if err := conn.WriteJSON(map[string]string{"type": "ambient_stop"}); err != nil {
		t.Fatal(err)
	}
	for message.Type != listeningStoppedMessageType {
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDiagnosticsSharesApplicationClipAdmission(t *testing.T) {
	app := NewServer(nil, Handlers{CandidateMaxConcurrent: 1, CandidateAudio: func(context.Context, []byte, stt.AudioFormat) (string, error) { return "", nil }})
	diagnostics := app.DiagnosticsServer(nil)
	first, second, extra := &candidateJob{}, &candidateJob{}, &candidateJob{}
	if !app.tryAdmitCandidate(first) || !diagnostics.tryAdmitCandidate(second) {
		t.Fatal("expected two retained clips within shared capacity")
	}
	if app.tryAdmitCandidate(extra) || diagnostics.tryAdmitCandidate(extra) {
		t.Fatal("diagnostics bypassed application clip capacity")
	}
	app.releaseCandidateAdmission(*first)
	diagnostics.releaseCandidateAdmission(*second)
}

func (diagnosticFactory) Open() (ambient.Stream, error) { return &diagnosticStream{}, nil }

type diagnosticStream struct{ blocks int }

func (s *diagnosticStream) Add([]float32) error { s.blocks++; return nil }
func (s *diagnosticStream) Transcript() ([]ambient.Line, error) {
	switch s.blocks {
	case 1:
		return []ambient.Line{{ID: 1, Text: "The weather is nice", Start: 0, Duration: .25, Complete: true}}, nil
	case 4:
		return []ambient.Line{{ID: 2, Text: "Remind me to buy milk", Start: .25, Duration: .75, Complete: true}}, nil
	}
	return nil, nil
}
func (*diagnosticStream) Reset() error { return nil }
func (*diagnosticStream) Close()       {}

func TestDiagnosticsShowsAllMoonshineAndSelectedDeepgramWithoutActions(t *testing.T) {
	listener := &ambient.Listener{Factory: diagnosticFactory{}}
	paidCalls := make(chan int, 2)
	s := NewServer(transcriberFunc(func(context.Context, <-chan []byte, chan<- string, stt.TranscriptObserver) error {
		t.Error("diagnostics opened paid live streaming")
		return nil
	}), Handlers{
		Diagnostics: true, Ambient: listener.Run, AmbientObserved: listener.RunObserved,
		Authenticate: func(string) (tool.Scope, error) { return tool.Scope{UserID: "u", SessionID: "s"}, nil },
		CandidateAudio: func(_ context.Context, audio []byte, _ stt.AudioFormat) (string, error) {
			paidCalls <- len(audio)
			return "Buy milk tomorrow.", nil
		},
		Utterance: func(context.Context, tool.Scope, string) (UtteranceResult, error) {
			t.Error("test speech reached assistant actions")
			return UtteranceResult{}, nil
		},
	})
	server := httptest.NewServer(s)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err = conn.WriteJSON(map[string]string{"type": "ambient_start"}); err != nil {
		t.Fatal(err)
	}
	var message serverMessage
	if err = conn.ReadJSON(&message); err != nil || message.Type != "ready" {
		t.Fatalf("ready: %+v %v", message, err)
	}
	for i := 0; i < 4; i++ {
		if err = conn.WriteMessage(websocket.BinaryMessage, make([]byte, 32000)); err != nil {
			t.Fatal(err)
		}
	}
	moonshineCount, ack, accurate := 0, int64(0), false
	for !accurate || ack < 128000 {
		if err = conn.ReadJSON(&message); err != nil {
			t.Fatal(err)
		}
		switch message.Type {
		case "moonshine_transcript":
			moonshineCount++
		case "audio_received":
			ack = message.ReceivedBytes
		case "deepgram_transcript":
			accurate = message.Text == "Buy milk tomorrow."
		}
	}
	if moonshineCount != 2 {
		t.Fatalf("missing background/keyword transcripts: %d", moonshineCount)
	}
	if len(paidCalls) != 1 {
		t.Fatalf("Deepgram calls=%d", len(paidCalls))
	}
	if bytes := <-paidCalls; bytes != 96000 {
		t.Fatalf("selected PCM bytes=%d", bytes)
	}
	if err = conn.WriteJSON(map[string]string{"type": "ambient_stop"}); err != nil {
		t.Fatal(err)
	}
	for message.Type != listeningStoppedMessageType {
		if err = conn.ReadJSON(&message); err != nil {
			t.Fatal(err)
		}
	}
}
