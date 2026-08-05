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
	observe stt.TranscriptObserver,
) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case chunk, ok := <-audio:
			if !ok {
				return nil
			}
			if err := observe(string(chunk)); err != nil {
				return err
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
		) (UtteranceResult, error) {
			connection := ctx.Value(connectionKey{}).(string)
			received <- connection + ":" + scope.SessionID + ":" + utterance
			return UtteranceResult{}, nil
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
		) (UtteranceResult, error) {
			utteranceScopes <- scope
			return UtteranceResult{
				Text:                 "reply to " + utterance,
				AwaitingConfirmation: true,
				WorkspaceResources: []WorkspaceResource{
					WorkspaceConversations,
					WorkspaceTasks,
				},
			}, nil
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
	if err := conn.WriteJSON(map[string]string{"type": listeningStartMessageType}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("where am I")); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}

	assertServerMessage(t, conn, userTranscriptMessageType, "where am I")
	assertServerMessageType(t, conn, assistantThinkingMessageType)
	var change serverMessage
	if err := conn.ReadJSON(&change); err != nil {
		t.Fatalf("ReadJSON() workspace change error = %v", err)
	}
	if change.Type != workspaceChangedMessageType ||
		len(change.Resources) != 2 ||
		change.Resources[0] != WorkspaceConversations ||
		change.Resources[1] != WorkspaceTasks {
		t.Fatalf("workspace change = %+v", change)
	}
	var response serverMessage
	if err := conn.ReadJSON(&response); err != nil {
		t.Fatalf("ReadJSON() error = %v", err)
	}
	if response.Type != assistantResponseMessageType ||
		response.Text != "reply to where am I" ||
		!response.AwaitingConfirmation {
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
	if err := conn.WriteJSON(map[string]string{"type": listeningStopMessageType}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	assertServerMessageType(t, conn, listeningStoppedMessageType)

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

func TestServerStopsThinkingWhenUtteranceHasNoResponse(t *testing.T) {
	server := NewServer(echoTranscriber{}, Handlers{
		Authenticate: func(string) (tool.Scope, error) {
			return tool.Scope{UserID: "user", SessionID: "session"}, nil
		},
		Utterance: func(context.Context, tool.Scope, string) (UtteranceResult, error) {
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
	if err := conn.WriteJSON(map[string]string{"type": listeningStartMessageType}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("never mind")); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}

	assertServerMessage(t, conn, userTranscriptMessageType, "never mind")
	assertServerMessageType(t, conn, assistantThinkingMessageType)
	assertServerMessageType(t, conn, assistantDoneMessageType)
}

func TestServerHandlesNotificationAcknowledgement(t *testing.T) {
	expectedScope := tool.Scope{UserID: "user", SessionID: "session"}
	type acknowledgement struct {
		scope          tool.Scope
		notificationID string
	}
	acknowledged := make(chan acknowledgement, 1)
	server := NewServer(echoTranscriber{}, Handlers{
		Authenticate: func(string) (tool.Scope, error) {
			return expectedScope, nil
		},
		NotificationAck: func(
			_ context.Context,
			scope tool.Scope,
			notificationID string,
		) error {
			acknowledged <- acknowledgement{
				scope:          scope,
				notificationID: notificationID,
			}
			return nil
		},
	})
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, _, err := websocket.DefaultDialer.Dial(websocketTestURL(httpServer.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]string{
		"type": notificationAckMessageType,
		"id":   "notification-1",
	}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}

	got := receive(t, acknowledged)
	if got.scope != expectedScope || got.notificationID != "notification-1" {
		t.Fatalf("acknowledgement = %+v", got)
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
		Utterance: func(
			ctx context.Context,
			_ tool.Scope,
			_ string,
		) (UtteranceResult, error) {
			close(started)
			<-ctx.Done()
			close(canceled)
			return UtteranceResult{}, ctx.Err()
		},
	})
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, _, err := websocket.DefaultDialer.Dial(websocketTestURL(httpServer.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	if err := conn.WriteJSON(map[string]string{"type": listeningStartMessageType}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
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

func TestServerControlsTranscriptionLifecycle(t *testing.T) {
	started := make(chan struct{}, 2)
	processed := make(chan struct{}, 1)
	server := NewServer(transcriberFunc(func(
		ctx context.Context,
		audio <-chan []byte,
		completed chan<- string,
		observe stt.TranscriptObserver,
	) error {
		started <- struct{}{}
		return echoTranscriber{}.Transcribe(ctx, audio, completed, observe)
	}), Handlers{
		Authenticate: func(string) (tool.Scope, error) {
			return tool.Scope{UserID: "user", SessionID: "session"}, nil
		},
		Location: func(context.Context, tool.Scope, LocationUpdate) error {
			processed <- struct{}{}
			return nil
		},
		Utterance: func(
			_ context.Context,
			_ tool.Scope,
			utterance string,
		) (UtteranceResult, error) {
			return UtteranceResult{Text: "reply to " + utterance}, nil
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
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("ignored")); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
	if err := conn.WriteJSON(map[string]string{"type": locationMessageType}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	receive(t, processed)

	select {
	case <-started:
		t.Fatal("transcription started on connection")
	default:
	}

	for _, utterance := range []string{"first", "second"} {
		if err := conn.WriteJSON(map[string]string{"type": listeningStartMessageType}); err != nil {
			t.Fatalf("WriteJSON() error = %v", err)
		}
		receive(t, started)

		if err := conn.WriteMessage(websocket.BinaryMessage, []byte(utterance)); err != nil {
			t.Fatalf("WriteMessage() error = %v", err)
		}
		assertServerMessage(t, conn, userTranscriptMessageType, utterance)
		assertServerMessageType(t, conn, assistantThinkingMessageType)
		var response serverMessage
		if err := conn.ReadJSON(&response); err != nil {
			t.Fatalf("ReadJSON() error = %v", err)
		}
		if response.Type != assistantResponseMessageType || response.Text != "reply to "+utterance {
			t.Fatalf("response = %+v", response)
		}

		if err := conn.WriteJSON(map[string]string{"type": listeningStopMessageType}); err != nil {
			t.Fatalf("WriteJSON() error = %v", err)
		}
		assertServerMessageType(t, conn, listeningStoppedMessageType)
	}
}

type discardJSONWriter struct{}

func (discardJSONWriter) WriteJSON(any) error {
	return nil
}

type transcriberFunc func(
	context.Context,
	<-chan []byte,
	chan<- string,
	stt.TranscriptObserver,
) error

func (f transcriberFunc) Transcribe(
	ctx context.Context,
	audio <-chan []byte,
	completed chan<- string,
	observe stt.TranscriptObserver,
) error {
	return f(ctx, audio, completed, observe)
}

func assertServerMessageType(t *testing.T, conn *websocket.Conn, want string) {
	t.Helper()

	var message serverMessage
	if err := conn.ReadJSON(&message); err != nil {
		t.Fatalf("ReadJSON() error = %v", err)
	}
	if message.Type != want {
		t.Fatalf("message type = %q, want %q", message.Type, want)
	}
}

func assertServerMessage(
	t *testing.T,
	conn *websocket.Conn,
	wantType string,
	wantText string,
) {
	t.Helper()

	var message serverMessage
	if err := conn.ReadJSON(&message); err != nil {
		t.Fatalf("ReadJSON() error = %v", err)
	}
	if message.Type != wantType || message.Text != wantText {
		t.Fatalf(
			"message = %+v, want type %q and text %q",
			message,
			wantType,
			wantText,
		)
	}
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
