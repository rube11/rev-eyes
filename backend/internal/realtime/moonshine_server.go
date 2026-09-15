package realtime

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rube11/rev-eyes/backend/internal/candidate"
	"github.com/rube11/rev-eyes/backend/internal/stt"
)

// MoonshineServer is the continuous PCM diagnostics path. Inference runs in a
// Go-owned native Moonshine subprocess; only server-selected speech reaches STT.
type MoonshineServer struct {
	Python, Worker string
	Authenticate   Authenticator
	CheckOrigin    func(*http.Request) bool
	Accurate       CandidateAudioHandler
	permit         chan struct{}
}

func NewMoonshineServer(python, worker string, auth Authenticator, origin func(*http.Request) bool, accurate CandidateAudioHandler) *MoonshineServer {
	return &MoonshineServer{Python: python, Worker: worker, Authenticate: auth, CheckOrigin: origin, Accurate: accurate, permit: make(chan struct{}, 1)}
}

type moonshineEvent struct {
	Type          string `json:"type"`
	ID            string `json:"id,omitempty"`
	Text          string `json:"text,omitempty"`
	Final         bool   `json:"final,omitempty"`
	PCM           []byte `json:"pcm,omitempty"`
	Error         string `json:"error,omitempty"`
	ReceivedBytes int64  `json:"received_bytes,omitempty"`
}

func (s *MoonshineServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.Python == "" || s.Worker == "" || s.Accurate == nil {
		http.Error(w, "Server Moonshine unavailable", http.StatusServiceUnavailable)
		return
	}
	if s.Authenticate == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if _, err := s.Authenticate(r.URL.Query().Get("ticket")); err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	select {
	case s.permit <- struct{}{}:
		defer func() { <-s.permit }()
	default:
		http.Error(w, "Another Moonshine test is running", http.StatusServiceUnavailable)
		return
	}
	upgrader := websocket.Upgrader{CheckOrigin: s.CheckOrigin}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(32000)
	ctx, cancel := context.WithTimeout(r.Context(), 7*time.Minute)
	defer cancel()
	var writeMu sync.Mutex
	write := func(event moonshineEvent) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		err := conn.WriteJSON(event)
		if err != nil {
			cancel()
		}
		return err
	}
	cmd := exec.CommandContext(ctx, s.Python, "-u", s.Worker)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		write(moonshineEvent{Type: "error", Error: "Could not start server Moonshine"})
		return
	}
	go func() { <-ctx.Done(); conn.Close() }()
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer stdin.Close()
		var total, acknowledged int64
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				cancel()
				return
			}
			if kind == websocket.TextMessage {
				var control struct {
					Type string `json:"type"`
				}
				if json.Unmarshal(data, &control) == nil && control.Type == "listening_stop" {
					return
				}
				continue
			}
			if kind != websocket.BinaryMessage {
				continue
			}
			if len(data) == 0 || len(data)%2 != 0 {
				cancel()
				return
			}
			total += int64(len(data))
			if total > 7*60*32000 {
				cancel()
				return
			}
			var header [4]byte
			binary.LittleEndian.PutUint32(header[:], uint32(len(data)))
			_, err = stdin.Write(header[:])
			if err == nil {
				_, err = stdin.Write(data)
			}
			clearCandidateAudio(data)
			if err != nil {
				cancel()
				return
			}
			if total-acknowledged >= 32000 {
				acknowledged = total
				if write(moonshineEvent{Type: "audio_received", ReceivedBytes: total}) != nil {
					return
				}
			}
		}
	}()
	jobs := make(chan moonshineEvent, 2)
	accurateDone := make(chan struct{})
	go func() {
		defer close(accurateDone)
		for event := range jobs {
			if ctx.Err() != nil {
				clearCandidateAudio(event.PCM)
				continue
			}
			clipCtx, clipCancel := context.WithTimeout(ctx, 25*time.Second)
			text, err := s.Accurate(clipCtx, event.PCM, stt.AudioFormat{Encoding: stt.EncodingLinear16, SampleRate: 16000, Channels: 1})
			clipCancel()
			clearCandidateAudio(event.PCM)
			if err != nil {
				slog.WarnContext(ctx, "server Moonshine Deepgram clip failed", "error", err)
				write(moonshineEvent{Type: "error", ID: event.ID, Error: "Deepgram transcription failed"})
			} else if text != "" {
				write(moonshineEvent{Type: "deepgram_transcript", ID: event.ID, Text: text, Final: true})
			}
		}
	}()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	for scanner.Scan() {
		var event moonshineEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			err = fmt.Errorf("invalid Moonshine worker output: %w", err)
			slog.ErrorContext(ctx, "server Moonshine protocol failed", "error", err)
			cancel()
			break
		}
		pcm := event.PCM
		event.PCM = nil
		if event.Type != "stopped" {
			write(event)
		}
		if event.Type == "moonshine_transcript" && event.Final && event.Error == "" && len(pcm) > 0 {
			if reason, matched := candidate.MatchWakePhrase(event.Text); matched {
				event.PCM = pcm
				select {
				case jobs <- event:
					slog.InfoContext(ctx, "server Moonshine selected clip", "wake_reason", reason, "bytes", len(pcm))
					write(moonshineEvent{Type: "keyword_detected", ID: event.ID, Text: string(reason)})
					continue
				default:
					write(moonshineEvent{Type: "error", Error: "Deepgram is busy; this clip was not submitted"})
				}
			}
		}
		clearCandidateAudio(pcm)
	}
	if scanner.Err() != nil {
		cancel()
	}
	err = cmd.Wait()
	close(jobs)
	<-accurateDone
	if err != nil && ctx.Err() == nil {
		write(moonshineEvent{Type: "error", Error: "Server Moonshine stopped unexpectedly"})
	}
	if ctx.Err() == nil {
		write(moonshineEvent{Type: "stopped"})
	}
	cancel()
	<-readerDone
}
