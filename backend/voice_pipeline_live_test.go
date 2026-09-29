//go:build moonshine && cgo

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rube11/rev-eyes/backend/internal/ambient"
	"github.com/rube11/rev-eyes/backend/internal/assistant"
	"github.com/rube11/rev-eyes/backend/internal/assistant/jev"
	"github.com/rube11/rev-eyes/backend/internal/assistant/openai"
	"github.com/rube11/rev-eyes/backend/internal/assistant/openai/tooling"
	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/realtime"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/speech"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/stt/moonshine"
	"github.com/rube11/rev-eyes/backend/internal/tool"
	"github.com/rube11/rev-eyes/backend/internal/tool/location"
	"github.com/rube11/rev-eyes/backend/internal/tool/websearch"
)

// All persistence stays in memory. The weather scenario additionally enables
// real read-only search over a synthetic Seattle device location. No account
// mutation tools are registered in either scenario.
type liveVoiceState struct {
	mu       sync.Mutex
	messages []session.Message
}

func (s *liveVoiceState) Append(_ context.Context, _ tool.Scope, speaker session.Speaker, text string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := fmt.Sprintf("synthetic-%d", len(s.messages)+1)
	s.messages = append(s.messages, session.Message{ID: id, Speaker: speaker, Text: text})
	return id, nil
}
func (s *liveVoiceState) Prepare(_ context.Context, _ tool.Scope, current, _ string) (session.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var conversation session.Conversation
	for _, message := range s.messages {
		if message.ID != current {
			conversation.Messages = append(conversation.Messages, message)
		}
	}
	return conversation, nil
}
func (*liveVoiceState) Find(context.Context, tool.Scope, memory.Lookup) ([]memory.Card, error) {
	return nil, nil
}
func (*liveVoiceState) Review(context.Context, tool.Scope, memory.Lookup) ([]memory.Card, error) {
	return nil, nil
}
func (*liveVoiceState) Profile(context.Context, tool.Scope) (string, error) { return "", nil }
func (*liveVoiceState) Forget(context.Context, tool.Scope, memory.Lookup) (int, error) {
	return 0, errors.New("mutations disabled in voice smoke test")
}
func (*liveVoiceState) SetProfileOverride(context.Context, tool.Scope, memory.Lookup, memory.ProfileLayer) (int, error) {
	return 0, errors.New("mutations disabled in voice smoke test")
}
func (*liveVoiceState) Confirm(context.Context, tool.Scope, string) (string, bool, error) {
	return "", false, nil
}

type liveVoiceTranscriber struct {
	stt.Transcriber
	live  stt.ConversationTranscriber
	calls atomic.Int32
}

func (s *liveVoiceTranscriber) TranscribeConversation(ctx context.Context, audio <-chan stt.AudioInput, completed chan<- stt.Utterance, observe stt.TranscriptObserver) error {
	s.calls.Add(1)
	return s.live.TranscribeConversation(ctx, audio, completed, observe)
}

type liveVoiceReadOnlyTool struct {
	tool.Tool
	calls    atomic.Int32
	failures atomic.Int32
}

func (t *liveVoiceReadOnlyTool) Execute(ctx context.Context, scope tool.Scope, args json.RawMessage) (tool.Result, error) {
	if !t.Spec().ReadOnly {
		return tool.Result{}, errors.New("live voice test forbids mutating tools")
	}
	t.calls.Add(1)
	result, err := t.Tool.Execute(ctx, scope, args)
	if err != nil {
		t.failures.Add(1)
	}
	return result, err
}

func liveVoiceWeatherWorkflow(t *testing.T, client *jev.Client, scope tool.Scope) (*assistant.ToolWorkflow, []*liveVoiceReadOnlyTool) {
	t.Helper()
	locations := location.NewStore()
	if err := locations.Update(scope, location.Position{Latitude: 47.6062, Longitude: -122.3321, AccuracyMeters: 10}); err != nil {
		t.Fatal(err)
	}
	loc, err := location.New(locations)
	if err != nil {
		t.Fatal(err)
	}
	search, err := websearch.New(os.Getenv("TAVILY_API_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry()
	observed := []*liveVoiceReadOnlyTool{{Tool: loc}, {Tool: search}}
	for _, wrapped := range observed {
		if err := registry.Register(wrapped); err != nil {
			t.Fatal(err)
		}
	}
	classifier, err := assistant.NewJevToolClassifier(client)
	if err != nil {
		t.Fatal(err)
	}
	builder, err := tooling.NewArgumentBuilder(os.Getenv("OPENAI_API_KEY"), os.Getenv("OPENAI_ROUTER_MODEL"))
	if err != nil {
		t.Fatal(err)
	}
	workflow, err := assistant.NewToolWorkflow(registry, classifier, builder)
	if err != nil {
		t.Fatal(err)
	}
	return workflow, observed
}

// Supply 16 kHz mono PCM16LE: "Hey glasses, what is two plus two?" followed by
// "And three plus three?" (no wake phrase). This verifies a real generated answer, not an
// echo handler. VOICE_PIPELINE_SCENARIO=weather instead accepts a single spoken
// local weather request and tests real location/search. Set
// VOICE_PIPELINE_NO_SILENCE=1 to send no PCM after speech, reproducing a microphone
// transport gap. It does not validate the physical SDK/display or database.
// VOICE_PIPELINE_SPEAKER_ROLE=other or mixed exercises fallible SDK attribution.
func TestLiveVoicePipelineAnswersAndPersists(t *testing.T) {
	if os.Getenv("RUN_LIVE_VOICE_PIPELINE_TEST") != "1" {
		t.Skip("set RUN_LIVE_VOICE_PIPELINE_TEST=1 with Moonshine and synthetic math PCM fixtures")
	}
	first, err := os.ReadFile(os.Getenv("MOONSHINE_TEST_PCM"))
	if err != nil {
		t.Fatal(err)
	}
	weather := os.Getenv("VOICE_PIPELINE_SCENARIO") == "weather"
	noSilence := os.Getenv("VOICE_PIPELINE_NO_SILENCE") == "1"
	speakerRole := os.Getenv("VOICE_PIPELINE_SPEAKER_ROLE")
	if speakerRole != "" && speakerRole != "self" && speakerRole != "other" && speakerRole != "mixed" {
		t.Fatal("VOICE_PIPELINE_SPEAKER_ROLE must be self, other, or mixed")
	}
	var followup []byte
	if !weather {
		followup, err = os.ReadFile(os.Getenv("MOONSHINE_TEST_FOLLOWUP_PCM"))
		if err != nil {
			t.Fatal(err)
		}
	}
	factory, err := moonshine.New(os.Getenv("MOONSHINE_TEST_MODEL_DIR"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer factory.Close()
	dg, err := stt.NewDeepgramTranscriber(os.Getenv("DEEPGRAM_API_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	jevClient, err := jev.New(os.Getenv("JEV_API_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	router, err := assistant.NewJevRouter(jevClient)
	if err != nil {
		t.Fatal(err)
	}
	scope := tool.Scope{UserID: "synthetic-live-voice", SessionID: "synthetic-live-voice", TimeZone: "America/Los_Angeles"}
	// Math needs no tools; nil is the supported text-only composer mode.
	var workflow assistant.ToolRunner
	var observedTools []*liveVoiceReadOnlyTool
	if weather {
		workflow, observedTools = liveVoiceWeatherWorkflow(t, jevClient, scope)
	}
	agent, err := openai.NewAgent(os.Getenv("OPENAI_API_KEY"), os.Getenv("OPENAI_AGENT_MODEL"), workflow)
	if err != nil {
		t.Fatal(err)
	}
	state := &liveVoiceState{}
	service := assistant.NewService(router, agent, state, state, state)
	transcriber := &liveVoiceTranscriber{Transcriber: dg, live: dg}
	listener := &ambient.Listener{Factory: factory}
	started := time.Now()
	var speechEnded atomic.Int64
	afterSpeech := func() time.Duration {
		if end := speechEnded.Load(); end != 0 {
			return time.Since(time.Unix(0, end)).Round(time.Millisecond)
		}
		return 0
	}
	app := realtime.NewServer(transcriber, realtime.Handlers{
		ConversationTranscriber: transcriber, Ambient: listener.Run, AmbientStreaming: listener.RunStreaming,
		CandidateAudio: func(context.Context, []byte, stt.AudioFormat) (string, error) {
			return "", errors.New("unexpected prerecorded transcription")
		},
		Authenticate: func(string) (tool.Scope, error) {
			return scope, nil
		},
		Utterance: func(ctx context.Context, scope tool.Scope, text string) (realtime.UtteranceResult, error) {
			t.Logf("finalized after_start=%s after_speech=%s text=%q", time.Since(started).Round(time.Millisecond), afterSpeech(), text)
			return handleUtterance(ctx, scope, text, service, state, fakeMemoryService{})
		},
	})
	server := httptest.NewServer(app)
	defer server.Close()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := app.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	}()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?ticket=synthetic", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	conn.SetReadDeadline(time.Now().Add(75 * time.Second))
	if err := conn.WriteJSON(map[string]string{"type": "ambient_start", "encoding": "pcm_speaker_v1"}); err != nil {
		t.Fatal(err)
	}
	firstAnswer := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		frames := 0
		send := func(pcm []byte) error {
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return ctx.Err()
			}
			role := speech.Self
			if speakerRole == "other" || (speakerRole == "mixed" && frames/50%2 == 0) {
				role = speech.Other
			}
			frames++
			frame := append([]byte{1, byte(role)}, pcm...)
			return conn.WriteMessage(websocket.BinaryMessage, frame)
		}
		clip := func(pcm []byte) error {
			for i := 0; i < len(pcm); i += 640 {
				if err := send(pcm[i:min(i+640, len(pcm))]); err != nil {
					return err
				}
			}
			speechEnded.Store(time.Now().UnixNano())
			return nil
		}
		if err := clip(first); err != nil {
			writerDone <- err
			return
		}
		silence := make([]byte, 640)
		// Continue forwarding silent audio, just as an uninterrupted microphone
		// does, so response latency cannot accidentally starve endpointing.
		for {
			if noSilence {
				select {
				case <-firstAnswer:
					goto followup
				case <-ctx.Done():
					writerDone <- ctx.Err()
					return
				}
			}
			select {
			case <-firstAnswer:
				goto followup
			default:
			}
			if err := send(silence); err != nil {
				writerDone <- err
				return
			}
		}
	followup:
		if weather {
			<-ctx.Done()
			writerDone <- ctx.Err()
			return
		}
		if err := clip(followup); err != nil {
			writerDone <- err
			return
		}
		for {
			if noSilence {
				<-ctx.Done()
				writerDone <- ctx.Err()
				return
			}
			if err := send(silence); err != nil {
				writerDone <- err
				return
			}
		}
	}()
	defer func() {
		cancel()
		if err := <-writerDone; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	}()
	responses, workspaceEvents := 0, 0
	wants := []*regexp.Regexp{regexp.MustCompile(`(?i)\b(?:4|four)\b`), regexp.MustCompile(`(?i)\b(?:6|six)\b`)}
	wantResponses := 2
	if weather {
		wantResponses = 1
	}
	for responses < wantResponses {
		var message struct {
			Type  string `json:"type"`
			Text  string `json:"text"`
			Error string `json:"error"`
		}
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatal(err)
		}
		if message.Error != "" {
			t.Fatal(message.Error)
		}
		t.Logf("event=%s after_start=%s after_speech=%s text=%q", message.Type, time.Since(started).Round(time.Millisecond), afterSpeech(), message.Text)
		switch message.Type {
		case "workspace_changed":
			workspaceEvents++
		case "assistant_response":
			if strings.TrimSpace(message.Text) == "" || strings.HasPrefix(message.Text, "Received:") || (!weather && !wants[responses].MatchString(message.Text)) {
				t.Fatalf("turn %d did not answer math question: %q", responses+1, message.Text)
			}
			responses++
			if responses == 1 {
				close(firstAnswer)
			}
		case "assistant_done", "conversation_idle", "listening_stopped":
			t.Fatalf("no answer before terminal event %s; responses=%d", message.Type, responses)
		}
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.messages) != 2*wantResponses || workspaceEvents != wantResponses || transcriber.calls.Load() != 1 {
		t.Fatalf("saved=%d workspace_events=%d Deepgram_connections=%d", len(state.messages), workspaceEvents, transcriber.calls.Load())
	}
	for i, message := range state.messages {
		want := session.SpeakerUser
		if i%2 == 1 {
			want = session.SpeakerAssistant
		}
		if message.Speaker != want {
			t.Errorf("saved message %d speaker=%s want=%s", i, message.Speaker, want)
		}
	}
	for _, observed := range observedTools {
		t.Logf("read_only_tool=%s executions=%d failures=%d", observed.Spec().Name, observed.calls.Load(), observed.failures.Load())
		if observed.calls.Load() != 1 || observed.failures.Load() != 0 {
			t.Errorf("weather needs one successful %s execution, got calls=%d failures=%d", observed.Spec().Name, observed.calls.Load(), observed.failures.Load())
		}
	}
	t.Logf("Pipeline summary: answers=%d saved=%d workspace_updates=%d no_silence=%t weather_tools=%t speaker_role=%q. Database and physical display excluded; forecast accuracy is not graded.", responses, len(state.messages), workspaceEvents, noSilence, weather, speakerRole)
}
