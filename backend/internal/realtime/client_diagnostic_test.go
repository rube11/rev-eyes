package realtime

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func TestClientDiagnosticValidation(t *testing.T) {
	t.Parallel()

	valid := []ClientDiagnostic{
		{Event: "transcript", Kind: "partial", Text: "hey glasses"},
		{Event: "lifecycle", Name: "speech started"},
		{
			Event:        "candidate_trigger",
			Category:     "assistant_request",
			Confidence:   0.78,
			SampleOffset: 16_000,
		},
		{
			Event:             "candidate_finalized",
			Reason:            "endpoint",
			Category:          "commitment",
			ByteLength:        32_000,
			StartSampleOffset: 1,
			EndSampleOffset:   16_001,
			Submitted:         true,
		},
	}
	for _, diagnostic := range valid {
		if _, err := diagnostic.normalized(); err != nil {
			t.Errorf("normalized(%+v) error = %v", diagnostic, err)
		}
	}

	invalid := []ClientDiagnostic{
		{},
		{Event: "transcript", Kind: "partial"},
		{Event: "transcript", Kind: "unknown", Text: "hello"},
		{Event: "lifecycle", Name: "speech\nstarted"},
		{Event: "candidate_trigger", Category: "background", Confidence: 1},
		{
			Event:             "candidate_finalized",
			Reason:            "endpoint",
			Category:          "commitment",
			ByteLength:        32_000,
			StartSampleOffset: 2,
			EndSampleOffset:   1,
		},
	}
	for _, diagnostic := range invalid {
		if _, err := diagnostic.normalized(); !errors.Is(err, errClientDiagnosticInvalid) {
			t.Errorf("normalized(%+v) error = %v, want invalid", diagnostic, err)
		}
	}
}

func TestServerForwardsValidatedClientDiagnostics(t *testing.T) {
	diagnostics := make(chan ClientDiagnostic, 1)
	server := NewServer(echoTranscriber{}, Handlers{
		Authenticate: func(string) (tool.Scope, error) {
			return tool.Scope{UserID: "user", SessionID: "session"}, nil
		},
		ClientDiagnostic: func(_ context.Context, diagnostic ClientDiagnostic) {
			diagnostics <- diagnostic
		},
	})
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, _, err := websocket.DefaultDialer.Dial(websocketTestURL(httpServer.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]any{
		"type": moonshineDiagnosticMessageType,
		"diagnostic": map[string]any{
			"event": "transcript",
			"kind":  "committed",
			"text":  "  hey glasses, what time is it?  ",
		},
	}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}

	got := receive(t, diagnostics)
	if got.Event != "transcript" ||
		got.Kind != "committed" ||
		got.Text != "hey glasses, what time is it?" {
		t.Fatalf("diagnostic = %+v", got)
	}
}

func TestClientDiagnosticLimiterResetsAfterWindow(t *testing.T) {
	t.Parallel()

	var limiter clientDiagnosticLimiter
	now := time.Unix(100, 0)
	for range diagnosticRateLimit {
		if !limiter.allow(now) {
			t.Fatal("limiter rejected an event within the configured burst")
		}
	}
	if limiter.allow(now) {
		t.Fatal("limiter accepted an event over the configured burst")
	}
	if !limiter.allow(now.Add(diagnosticRateWindow)) {
		t.Fatal("limiter did not reset after its window")
	}
}
