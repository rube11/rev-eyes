package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rube11/rev-eyes/backend/internal/ambient"
	"github.com/rube11/rev-eyes/backend/internal/assistant"
	"github.com/rube11/rev-eyes/backend/internal/candidate"
	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/realtime"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type ambientMemoryExtractor struct {
	started chan string
	release chan struct{}
}

func (e ambientMemoryExtractor) Extract(ctx context.Context, text string) ([]memory.Candidate, error) {
	e.started <- text
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.release:
	}
	return []memory.Candidate{{MemoryKey: "profile.food.preference.steak", Retention: memory.RetentionDurable,
		ProfileLayer: memory.ProfileDetail, Card: memory.Card{Topics: []memory.Topic{memory.TopicPreferences}, Title: "Food preference", Summary: "The user likes steak.", Kind: memory.KindPreference}}}, nil
}

type ambientMemoryWrite struct {
	scope      tool.Scope
	source     string
	candidates []memory.Candidate
}
type ambientMemoryWriter chan ambientMemoryWrite

func (w ambientMemoryWriter) RememberCandidates(_ context.Context, scope tool.Scope, source string, candidates []memory.Candidate) (int, error) {
	w <- ambientMemoryWrite{scope, source, candidates}
	return len(candidates), nil
}

// The audio transport must enter master's shared utterance path, including
// session preparation and memory learning even when routing ignores the speech.
func TestAmbientTranscriptLearnsMemoryAfterSocketDisconnect(t *testing.T) {
	const text = "Glasses, I like steak."
	scope := tool.Scope{UserID: "user", SessionID: "session"}
	extractor := ambientMemoryExtractor{make(chan string, 1), make(chan struct{})}
	stored := make(ambientMemoryWriter, 1)
	recorder, err := memory.NewRecorder(extractor, stored)
	if err != nil {
		t.Fatal(err)
	}
	appCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); recorder.Run(appCtx) }()
	defer func() { cancel(); <-workerDone }()
	prepared := false
	routed := false
	persisted := false
	service := fakeUtteranceService{handle: func(_ context.Context, got tool.Scope, source, transcript string) (assistant.Outcome, error) {
		if !prepared || !persisted || got != scope || source != "utterance" || transcript != text {
			t.Error("ambient transcript bypassed normal routing prerequisites")
		}
		routed = true
		return assistant.Outcome{Decision: assistant.Decision{Action: assistant.ActionIgnore}}, nil
	}}
	transcripts := fakeTranscriptStore{append: func(_ context.Context, got tool.Scope, speaker session.Speaker, transcript string) (string, error) {
		if !prepared || got != scope || speaker != session.SpeakerUser || transcript != text {
			t.Error("unexpected transcript persistence")
		}
		persisted = true
		return "utterance", nil
	}}
	s := realtime.NewServer(nil, realtime.Handlers{
		Authenticate: func(string) (tool.Scope, error) { return scope, nil },
		PrepareSession: func(_ context.Context, got tool.Scope) error {
			if got != scope {
				t.Error("wrong session")
			}
			prepared = true
			return nil
		},
		Ambient: func(ctx context.Context, input <-chan ambient.Input, emit func(ambient.Clip)) error {
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case event := <-input:
					if len(event.PCM) > 0 {
						emit(ambient.Clip{Audio: event.PCM, End: int64(len(event.PCM) / 2), Reason: candidate.WakeAssistantRequest})
					}
				}
			}
		},
		CandidateAudio: func(context.Context, []byte, stt.AudioFormat) (string, error) { return text, nil },
		Utterance: func(ctx context.Context, got tool.Scope, text string) (realtime.UtteranceResult, error) {
			return handleUtterance(ctx, got, text, service, transcripts, recorder)
		},
	})
	server := httptest.NewServer(s)
	defer server.Close()
	defer func() {
		ctx, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := s.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	}()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?ticket=test", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]string{"type": "ambient_start"}); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte{1, 0, 2, 0}); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		var response struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if err := conn.ReadJSON(&response); err != nil {
			t.Fatal(err)
		}
		if response.Type == "assistant_thinking" || response.Type == "assistant_response" {
			t.Fatal("ignored ambient utterance interrupted the user")
		}
		if response.Type == "assistant_done" {
			if response.ID == "" {
				t.Fatal("missing candidate correlation")
			}
			break
		}
	}
	select {
	case got := <-extractor.started:
		if got != text {
			t.Fatal("wrong memory input")
		}
	case <-time.After(time.Second):
		t.Fatal("memory extraction never started")
	}
	conn.Close()
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if !routed {
		t.Fatal("assistant routing bypassed")
	}
	close(extractor.release)
	select {
	case got := <-stored:
		if got.scope != scope || got.source != "utterance" || len(got.candidates) != 1 {
			t.Fatalf("wrong memory write: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("socket disconnect canceled application memory work")
	}
}
