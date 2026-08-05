package realtime

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func TestCandidateAudioHeaderValidation(t *testing.T) {
	valid := candidateAudioHeader{
		ID:                "candidate-1",
		Encoding:          candidateEncoding,
		SampleRate:        candidateSampleRate,
		Channels:          candidateChannels,
		ByteLength:        32_000,
		StartSampleOffset: 100,
		EndSampleOffset:   16_100,
		GateCategory:      "commitment",
		GateConfidence:    0.84,
	}
	if err := valid.validate(); err != nil {
		t.Fatalf("valid header error = %v", err)
	}
	manual := valid
	manual.GateCategory = "manual"
	if err := manual.validate(); err != nil {
		t.Fatalf("manual header error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*candidateAudioHeader)
	}{
		{"empty id", func(header *candidateAudioHeader) { header.ID = "" }},
		{"spaced id", func(header *candidateAudioHeader) { header.ID = " candidate-1 " }},
		{"invalid id character", func(header *candidateAudioHeader) { header.ID = "candidate/1" }},
		{"long id", func(header *candidateAudioHeader) { header.ID = strings.Repeat("a", maxCandidateIDLength+1) }},
		{"encoding", func(header *candidateAudioHeader) { header.Encoding = "opus" }},
		{"sample rate", func(header *candidateAudioHeader) { header.SampleRate = 48_000 }},
		{"channels", func(header *candidateAudioHeader) { header.Channels = 2 }},
		{"zero bytes", func(header *candidateAudioHeader) { header.ByteLength = 0 }},
		{"too many bytes", func(header *candidateAudioHeader) { header.ByteLength = maxCandidateAudioBytes + 2 }},
		{"negative start", func(header *candidateAudioHeader) { header.StartSampleOffset = -1 }},
		{"reversed range", func(header *candidateAudioHeader) { header.EndSampleOffset = header.StartSampleOffset }},
		{"length mismatch", func(header *candidateAudioHeader) { header.ByteLength -= 2 }},
		{"category", func(header *candidateAudioHeader) { header.GateCategory = "background" }},
		{"confidence", func(header *candidateAudioHeader) { header.GateConfidence = 1.1 }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header := valid
			test.mutate(&header)
			if err := header.validate(); !errors.Is(err, errCandidateHeaderInvalid) {
				t.Fatalf("validate() error = %v, want errCandidateHeaderInvalid", err)
			}
		})
	}
}

func TestCandidateIDWindowEvictsOldestID(t *testing.T) {
	window := newCandidateIDWindow()
	for index := 0; index < recentCandidateIDCapacity; index++ {
		window.Add(fmt.Sprintf("candidate-%d", index))
	}
	if !window.Contains("candidate-0") {
		t.Fatal("window lost an id before reaching capacity")
	}

	window.Add("candidate-new")
	if window.Contains("candidate-0") {
		t.Fatal("window retained its oldest id after capacity was exceeded")
	}
	if !window.Contains("candidate-new") {
		t.Fatal("window did not retain the newest id")
	}
	if window.size != recentCandidateIDCapacity {
		t.Fatalf("window size = %d, want %d", window.size, recentCandidateIDCapacity)
	}
}

func TestServerProcessesCandidateWithoutPublishingRoughTranscript(t *testing.T) {
	expectedScope := tool.Scope{UserID: "user", SessionID: "session"}
	accurateTranscript := "I need to go to the gym tomorrow after class."
	utterances := make(chan string, 1)
	server := NewServer(echoTranscriber{}, Handlers{
		Authenticate: func(string) (tool.Scope, error) {
			return expectedScope, nil
		},
		CandidateAudio: func(
			_ context.Context,
			_ []byte,
			format stt.AudioFormat,
		) (string, error) {
			if format != (stt.AudioFormat{
				Encoding:   stt.EncodingLinear16,
				SampleRate: 16_000,
				Channels:   1,
			}) {
				t.Fatalf("format = %+v", format)
			}
			return accurateTranscript, nil
		},
		Utterance: func(
			_ context.Context,
			scope tool.Scope,
			utterance string,
		) (UtteranceResult, error) {
			if scope != expectedScope {
				t.Fatalf("scope = %+v", scope)
			}
			utterances <- utterance
			return UtteranceResult{Text: "Should I create that reminder?"}, nil
		},
	})

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	conn, _, err := websocket.DefaultDialer.Dial(websocketTestURL(httpServer.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}

	audio := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	writeCandidate(t, conn, "candidate-1", audio)
	var response serverMessage
	if err := conn.ReadJSON(&response); err != nil {
		t.Fatalf("ReadJSON() error = %v", err)
	}
	if response.Type != assistantResponseMessageType ||
		response.ID != "candidate-1" ||
		response.Text != "Should I create that reminder?" {
		t.Fatalf("response = %+v", response)
	}

	if utterance := receive(t, utterances); utterance != accurateTranscript {
		t.Fatalf("utterance = %q", utterance)
	}
}

func TestServerRejectsAccurateTranscriptWithoutApprovedWakePhrase(t *testing.T) {
	utterances := make(chan string, 1)
	server := NewServer(echoTranscriber{}, Handlers{
		Authenticate: func(string) (tool.Scope, error) {
			return tool.Scope{UserID: "user", SessionID: "session"}, nil
		},
		CandidateAudio: func(
			_ context.Context,
			_ []byte,
			_ stt.AudioFormat,
		) (string, error) {
			return "The meeting starts at three.", nil
		},
		Utterance: func(
			_ context.Context,
			_ tool.Scope,
			utterance string,
		) (UtteranceResult, error) {
			utterances <- utterance
			return UtteranceResult{}, nil
		},
	})

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	conn, _, err := websocket.DefaultDialer.Dial(websocketTestURL(httpServer.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}

	writeCandidate(t, conn, "candidate-1", []byte{1, 2, 3, 4})
	var response serverMessage
	if err := conn.ReadJSON(&response); err != nil {
		t.Fatalf("ReadJSON() error = %v", err)
	}
	if response.Type != assistantDoneMessageType || response.ID != "candidate-1" {
		t.Fatalf("response = %+v, want assistant_done for candidate-1", response)
	}
	select {
	case utterance := <-utterances:
		t.Fatalf("wake-policy miss reached utterance handler: %q", utterance)
	default:
	}
}

func TestServerAllowsManualCandidateWithoutWakePhrase(t *testing.T) {
	utterances := make(chan string, 1)
	server := NewServer(echoTranscriber{}, Handlers{
		Authenticate: func(string) (tool.Scope, error) {
			return tool.Scope{UserID: "user", SessionID: "session"}, nil
		},
		CandidateAudio: func(context.Context, []byte, stt.AudioFormat) (string, error) {
			return "Yes.", nil
		},
		Utterance: func(
			_ context.Context,
			_ tool.Scope,
			utterance string,
		) (UtteranceResult, error) {
			utterances <- utterance
			return UtteranceResult{}, nil
		},
	})

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	conn, _, err := websocket.DefaultDialer.Dial(websocketTestURL(httpServer.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}

	writeCandidateWithCategory(t, conn, "candidate-1", []byte{1, 2, 3, 4}, "manual")
	assertServerMessageType(t, conn, assistantDoneMessageType)
	if utterance := receive(t, utterances); utterance != "Yes." {
		t.Fatalf("utterance = %q, want manual transcript", utterance)
	}
}

func TestServerRejectsCandidateLengthMismatch(t *testing.T) {
	called := make(chan struct{}, 1)
	server := NewServer(echoTranscriber{}, Handlers{
		Authenticate: func(string) (tool.Scope, error) {
			return tool.Scope{UserID: "user", SessionID: "session"}, nil
		},
		CandidateAudio: func(context.Context, []byte, stt.AudioFormat) (string, error) {
			called <- struct{}{}
			return "unexpected", nil
		},
	})
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	conn, _, err := websocket.DefaultDialer.Dial(websocketTestURL(httpServer.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}

	if err := conn.WriteJSON(candidateHeaderMessage("candidate-1", 8)); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte{1, 2}); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
	assertServerMessageType(t, conn, assistantDoneMessageType)
	select {
	case <-called:
		t.Fatal("candidate handler was called for a length mismatch")
	default:
	}
}

func TestServerRejectsDuplicateCandidateID(t *testing.T) {
	called := make(chan struct{}, 2)
	server := NewServer(echoTranscriber{}, Handlers{
		Authenticate: func(string) (tool.Scope, error) {
			return tool.Scope{UserID: "user", SessionID: "session"}, nil
		},
		CandidateAudio: func(context.Context, []byte, stt.AudioFormat) (string, error) {
			called <- struct{}{}
			return "", nil
		},
	})
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	conn, _, err := websocket.DefaultDialer.Dial(websocketTestURL(httpServer.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}

	audio := []byte{1, 2, 3, 4}
	writeCandidate(t, conn, "candidate-1", audio)
	assertServerMessageType(t, conn, assistantDoneMessageType)
	receive(t, called)

	writeCandidate(t, conn, "candidate-1", audio)
	assertServerMessageType(t, conn, assistantDoneMessageType)
	select {
	case <-called:
		t.Fatal("candidate handler was called for a duplicate id")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCandidateModeRejectsLegacyListeningStart(t *testing.T) {
	server := NewServer(echoTranscriber{}, Handlers{
		Authenticate: func(string) (tool.Scope, error) {
			return tool.Scope{UserID: "user", SessionID: "session"}, nil
		},
	})
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	conn, _, err := websocket.DefaultDialer.Dial(websocketTestURL(httpServer.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}

	if err := conn.WriteJSON(candidateHeaderMessage("candidate-1", 4)); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	if err := conn.WriteJSON(map[string]string{"type": listeningStartMessageType}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}

	var message serverMessage
	if err := conn.ReadJSON(&message); err != nil {
		t.Fatalf("ReadJSON() error = %v", err)
	}
	if message.Type != listeningStoppedMessageType || message.Error == "" {
		t.Fatalf("message = %+v, want listening stopped error", message)
	}
}

func TestServerClosesConnectionOnOverlappingCandidateHeaders(t *testing.T) {
	server := NewServer(echoTranscriber{}, Handlers{
		Authenticate: func(string) (tool.Scope, error) {
			return tool.Scope{UserID: "user", SessionID: "session"}, nil
		},
	})
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	conn, _, err := websocket.DefaultDialer.Dial(websocketTestURL(httpServer.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}

	if err := conn.WriteJSON(candidateHeaderMessage("candidate-1", 4)); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	if err := conn.WriteJSON(candidateHeaderMessage("candidate-2", 4)); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}

	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("connection remained open after ambiguous candidate framing")
	}
}

func TestCandidateProcessingIsCanceledOnDisconnect(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	server := NewServer(echoTranscriber{}, Handlers{
		Authenticate: func(string) (tool.Scope, error) {
			return tool.Scope{UserID: "user", SessionID: "session"}, nil
		},
		CandidateAudio: func(
			ctx context.Context,
			_ []byte,
			_ stt.AudioFormat,
		) (string, error) {
			close(started)
			<-ctx.Done()
			close(canceled)
			return "", ctx.Err()
		},
	})
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	conn, _, err := websocket.DefaultDialer.Dial(websocketTestURL(httpServer.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}

	writeCandidate(t, conn, "candidate-1", []byte{1, 2, 3, 4})
	receive(t, started)
	if err := conn.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	receive(t, canceled)
}

func writeCandidate(t *testing.T, conn *websocket.Conn, id string, audio []byte) {
	writeCandidateWithCategory(t, conn, id, audio, "commitment")
}

func writeCandidateWithCategory(
	t *testing.T,
	conn *websocket.Conn,
	id string,
	audio []byte,
	category string,
) {
	t.Helper()
	header := candidateHeaderMessage(id, len(audio))
	header["gate_category"] = category
	if err := conn.WriteJSON(header); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, audio); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
}

func candidateHeaderMessage(id string, byteLength int) map[string]any {
	return map[string]any{
		"type":                candidateAudioMessageType,
		"id":                  id,
		"encoding":            candidateEncoding,
		"sample_rate":         candidateSampleRate,
		"channels":            candidateChannels,
		"byte_length":         byteLength,
		"start_sample_offset": 0,
		"end_sample_offset":   byteLength / candidateBytesPerSample,
		"gate_category":       "commitment",
		"gate_confidence":     0.84,
	}
}

func allZero(audio []byte) bool {
	for _, value := range audio {
		if value != 0 {
			return false
		}
	}
	return true
}
