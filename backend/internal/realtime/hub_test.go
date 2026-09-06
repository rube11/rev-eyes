package realtime

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func TestHubSendsMessageWhileTranscriptionIdle(t *testing.T) {
	delivered := make(chan bool, 1)
	started := make(chan struct{}, 1)
	hub := NewHub()
	server := NewServerWithHub(transcriberFunc(func(
		context.Context,
		<-chan []byte,
		chan<- string,
		stt.TranscriptObserver,
	) error {
		started <- struct{}{}
		return nil
	}), hub, Handlers{
		Authenticate: func(string) (tool.Scope, error) {
			return tool.Scope{UserID: "user-1", SessionID: "session"}, nil
		},
		Connect: func(_ context.Context, scope tool.Scope) error {
			delivered <- hub.Send(scope.UserID, "notification-1", "Time to leave")
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
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if !receive(t, delivered) {
		t.Fatal("Send() did not deliver during connection setup")
	}
	if hub.Send("other-user", "notification-2", "not for this connection") {
		t.Fatal("Send() delivered to the wrong user")
	}

	var message serverMessage
	if err := conn.ReadJSON(&message); err != nil {
		t.Fatalf("ReadJSON() error = %v", err)
	}
	if message.Type != notificationMessageType ||
		message.ID != "notification-1" ||
		message.Text != "Time to leave" {
		t.Fatalf("message = %+v", message)
	}
	select {
	case <-started:
		t.Fatal("transcription started for outbound message")
	default:
	}
}

func TestHubSendsWorkspaceChanges(t *testing.T) {
	delivered := make(chan bool, 1)
	hub := NewHub()
	server := NewServerWithHub(transcriberFunc(func(
		context.Context,
		<-chan []byte,
		chan<- string,
		stt.TranscriptObserver,
	) error {
		return nil
	}), hub, Handlers{
		Authenticate: func(string) (tool.Scope, error) {
			return tool.Scope{UserID: "user-1", SessionID: "session"}, nil
		},
		Connect: func(_ context.Context, scope tool.Scope) error {
			delivered <- hub.WorkspaceChanged(
				scope.UserID,
				WorkspaceConversations,
				WorkspaceMemories,
			)
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
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if !receive(t, delivered) {
		t.Fatal("WorkspaceChanged() did not deliver during connection setup")
	}
	if hub.WorkspaceChanged("other-user", WorkspaceTasks) {
		t.Fatal("WorkspaceChanged() delivered to the wrong user")
	}

	var message serverMessage
	if err := conn.ReadJSON(&message); err != nil {
		t.Fatalf("ReadJSON() error = %v", err)
	}
	want := []WorkspaceResource{WorkspaceConversations, WorkspaceMemories}
	if message.Type != workspaceChangedMessageType ||
		len(message.Resources) != len(want) {
		t.Fatalf("message = %+v", message)
	}
	for index := range want {
		if message.Resources[index] != want[index] {
			t.Fatalf("resources = %#v, want %#v", message.Resources, want)
		}
	}
}
