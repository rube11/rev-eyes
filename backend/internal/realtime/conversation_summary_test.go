package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/speech"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type summaryTranscriber struct {
	utterances []stt.Utterance
}

func (*summaryTranscriber) Transcribe(context.Context, <-chan stt.AudioInput, chan<- stt.Utterance, stt.TranscriptObserver) error {
	return errors.New("unexpected per-turn stream")
}

func (f *summaryTranscriber) TranscribeConversation(ctx context.Context, _ <-chan stt.AudioInput, completed chan<- stt.Utterance, observe stt.TranscriptObserver) error {
	if err := observe("private interim transcript"); err != nil {
		return err
	}
	for _, utterance := range f.utterances {
		if err := observe(utterance.Text); err != nil {
			return err
		}
		select {
		case completed <- utterance:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestConversationSummaryDistinguishesMissingEndpointAndRejectedTurns(t *testing.T) {
	for _, finalized := range []bool{false, true} {
		t.Run(map[bool]string{false: "live_without_endpoint", true: "finalized_gate_decisions"}[finalized], func(t *testing.T) {
			t.Parallel()
			fake := &summaryTranscriber{}
			if finalized {
				fake.utterances = []stt.Utterance{
					{Text: "private background remark"},
					{Text: "Hey glasses, private other request", Segments: []speech.Segment{{Role: speech.Other}}},
					{Text: "Hey glasses, private wearer request", Segments: []speech.Segment{{Role: speech.Self}}},
				}
			}
			s := NewServer(fake, Handlers{ConversationTranscriber: fake, Utterance: func(context.Context, tool.Scope, string) (UtteranceResult, error) {
				return UtteranceResult{Text: "private assistant answer"}, nil
			}})
			s.conversationIdle = 10 * time.Millisecond
			var captured bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&captured, nil))
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			writer := conversationWriter{messages: make(chan serverMessage, 30)}
			if err := s.runConversationWithLogger(ctx, tool.Scope{UserID: "private-account-id", SessionID: "private-session-id"}, writer, nil, true, logger); err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(captured.String()), "\n")
			if len(lines) != 1 || lines[0] == "" {
				t.Fatalf("want one bounded conversation summary, got %q", captured.String())
			}
			var summary map[string]any
			if err := json.Unmarshal([]byte(lines[0]), &summary); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{
				"msg": "conversation summary", "automatic": true, "end_reason": "idle",
				"transcript_updates": float64(1), "finalized_utterances": float64(0),
				"accepted_utterances": float64(0), "rejected_wake": float64(0), "accepted_context_only": float64(0),
			}
			if finalized {
				want["transcript_updates"] = float64(4)
				want["finalized_utterances"] = float64(3)
				want["accepted_utterances"] = float64(2)
				want["rejected_wake"] = float64(1)
				want["accepted_context_only"] = float64(1)
			}
			for key, value := range want {
				if summary[key] != value {
					t.Errorf("%s=%v, want %v", key, summary[key], value)
				}
			}
			for _, key := range []string{"duration_ms", "first_transcript_ms", "first_finalized_ms"} {
				value, ok := summary[key].(float64)
				if !ok || value < -1 || ((key != "first_finalized_ms" || finalized) && value < 0) {
					t.Errorf("invalid timing %s=%v", key, summary[key])
				}
				if key == "first_finalized_ms" && !finalized && value != -1 {
					t.Errorf("missing endpoint must have first_finalized_ms=-1, got %v", value)
				}
			}
			for key := range summary {
				if _, ok := want[key]; ok {
					continue
				}
				switch key {
				case "time", "level", "duration_ms", "first_transcript_ms", "first_finalized_ms":
				default:
					t.Errorf("unexpected operational log field %q", key)
				}
			}
			if strings.Contains(captured.String(), "private") {
				t.Fatalf("summary leaked speech or identity: %s", captured.String())
			}
		})
	}
}
