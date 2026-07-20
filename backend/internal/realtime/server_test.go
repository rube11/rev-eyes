package realtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type echoTranscriber struct{}

func (echoTranscriber) Transcribe(
	ctx context.Context,
	audio <-chan []byte,
	completed chan<- string,
) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case chunk, ok := <-audio:
			if !ok {
				return nil
			}
			select {
			case completed <- string(chunk):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func TestTranscribeConnectionKeepsUtterancesWithConnectionContext(t *testing.T) {
	t.Parallel()

	type connectionKey struct{}
	received := make(chan string, 4)
	server := NewServer(echoTranscriber{}, Handlers{
		Utterance: func(
			ctx context.Context,
			scope tool.Scope,
			utterance string,
		) (string, error) {
			connection := ctx.Value(connectionKey{}).(string)
			received <- connection + ":" + scope.SessionID + ":" + utterance
			return "", nil
		},
	})

	audioA := make(chan []byte, 2)
	audioA <- []byte("first-a")
	audioA <- []byte("second-a")
	close(audioA)

	audioB := make(chan []byte, 2)
	audioB <- []byte("first-b")
	audioB <- []byte("second-b")
	close(audioB)

	errors := make(chan error, 2)
	go func() {
		ctx := context.WithValue(context.Background(), connectionKey{}, "a")
		errors <- server.transcribeConnection(
			ctx,
			tool.Scope{SessionID: "session-a"},
			discardJSONWriter{},
			audioA,
		)
	}()
	go func() {
		ctx := context.WithValue(context.Background(), connectionKey{}, "b")
		errors <- server.transcribeConnection(
			ctx,
			tool.Scope{SessionID: "session-b"},
			discardJSONWriter{},
			audioB,
		)
	}()

	for range 2 {
		if err := <-errors; err != nil {
			t.Fatalf("transcribeConnection() error = %v", err)
		}
	}

	utterances := make([]string, 0, 4)
	for range 4 {
		utterances = append(utterances, <-received)
	}
	sort.Strings(utterances)

	want := []string{
		"a:session-a:first-a",
		"a:session-a:second-a",
		"b:session-b:first-b",
		"b:session-b:second-b",
	}
	for i := range want {
		if utterances[i] != want[i] {
			t.Fatalf("utterances[%d] = %q, want %q", i, utterances[i], want[i])
		}
	}
}

func TestServerExchangesScopedLocationAndAssistantMessages(t *testing.T) {
	expectedScope := tool.Scope{
		UserID:    "user-123",
		SessionID: "session-456",
	}
	type scopedLocation struct {
		scope  tool.Scope
		update LocationUpdate
	}
	locations := make(chan scopedLocation, 1)
	utteranceScopes := make(chan tool.Scope, 1)
	disconnected := make(chan tool.Scope, 1)

	server := NewServer(echoTranscriber{}, Handlers{
		Authenticate: func(ticket string) (tool.Scope, error) {
			if ticket != "valid-ticket" {
				return tool.Scope{}, errors.New("invalid ticket")
			}
			return expectedScope, nil
		},
		Location: func(
			_ context.Context,
			scope tool.Scope,
			update LocationUpdate,
		) error {
			locations <- scopedLocation{scope: scope, update: update}
			return nil
		},
		Utterance: func(
			_ context.Context,
			scope tool.Scope,
			utterance string,
		) (string, error) {
			utteranceScopes <- scope
			return "reply to " + utterance, nil
		},
		Disconnect: func(scope tool.Scope) {
			disconnected <- scope
		},
	})

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, _, err := websocket.DefaultDialer.Dial(
		websocketTestURL(httpServer.URL)+"?ticket=valid-ticket",
		nil,
	)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}

	if err := conn.WriteJSON(map[string]any{
		"type":            locationMessageType,
		"latitude":        34.0522,
		"longitude":       -118.2437,
		"accuracy_meters": 8,
	}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("where am I")); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}

	var response serverMessage
	if err := conn.ReadJSON(&response); err != nil {
		t.Fatalf("ReadJSON() error = %v", err)
	}
	if response.Type != assistantResponseMessageType || response.Text != "reply to where am I" {
		t.Fatalf("response = %+v", response)
	}

	location := receive(t, locations)
	if location.scope != expectedScope ||
		location.update.Latitude != 34.0522 ||
		location.update.Longitude != -118.2437 ||
		location.update.AccuracyMeters != 8 {
		t.Fatalf("location = %+v", location)
	}
	if scope := receive(t, utteranceScopes); scope != expectedScope {
		t.Fatalf("utterance scope = %+v, want %+v", scope, expectedScope)
	}

	if err := conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
	); err != nil {
		t.Fatalf("close message error = %v", err)
	}
	if scope := receive(t, disconnected); scope != expectedScope {
		t.Fatalf("disconnect scope = %+v, want %+v", scope, expectedScope)
	}
}

func TestServerRejectsInvalidTicketBeforeUpgrade(t *testing.T) {
	server := NewServer(echoTranscriber{}, Handlers{
		Authenticate: func(string) (tool.Scope, error) {
			return tool.Scope{}, errors.New("invalid ticket")
		},
	})
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, response, err := websocket.DefaultDialer.Dial(websocketTestURL(httpServer.URL), nil)
	if err == nil {
		conn.Close()
		t.Fatal("Dial() error = nil")
	}
	if response == nil {
		t.Fatal("Dial() response = nil")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
}

func TestDisconnectCancelsUtterance(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	server := NewServer(echoTranscriber{}, Handlers{
		Authenticate: func(string) (tool.Scope, error) {
			return tool.Scope{UserID: "user", SessionID: "session"}, nil
		},
		Utterance: func(ctx context.Context, _ tool.Scope, _ string) (string, error) {
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
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("hello")); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
	receive(t, started)

	if err := conn.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	receive(t, canceled)
}

func TestServerRejectsOversizedMessage(t *testing.T) {
	disconnected := make(chan tool.Scope, 1)
	server := NewServer(echoTranscriber{}, Handlers{
		Authenticate: func(string) (tool.Scope, error) {
			return tool.Scope{UserID: "user", SessionID: "session"}, nil
		},
		Disconnect: func(scope tool.Scope) {
			disconnected <- scope
		},
	})
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, _, err := websocket.DefaultDialer.Dial(websocketTestURL(httpServer.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(
		websocket.BinaryMessage,
		make([]byte, maxMessageSize+1),
	); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
	receive(t, disconnected)
}

type discardJSONWriter struct{}

func (discardJSONWriter) WriteJSON(any) error {
	return nil
}

func websocketTestURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}

func receive[T any](t *testing.T, values <-chan T) T {
	t.Helper()

	select {
	case value := <-values:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for value")
		var zero T
		return zero
	}
}

var _ stt.Transcriber = echoTranscriber{}
