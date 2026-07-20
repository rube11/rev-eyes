package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const (
	completedUtteranceBuffer = 10
	maxMessageSize           = 1 << 20
)

const (
	locationMessageType          = "location"
	assistantResponseMessageType = "assistant_response"
)

type Authenticator func(ticket string) (tool.Scope, error)
type UtteranceHandler func(ctx context.Context, scope tool.Scope, utterance string) (string, error)
type LocationHandler func(ctx context.Context, scope tool.Scope, update LocationUpdate) error

type Handlers struct {
	Authenticate Authenticator
	CheckOrigin  func(r *http.Request) bool
	Utterance    UtteranceHandler
	Location     LocationHandler
	Disconnect   func(scope tool.Scope)
}

type LocationUpdate struct {
	Latitude       float64 `json:"latitude"`
	Longitude      float64 `json:"longitude"`
	AccuracyMeters float64 `json:"accuracy_meters,omitempty"`
}

type clientMessage struct {
	Type string `json:"type"`
	LocationUpdate
}

type serverMessage struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type jsonWriter interface {
	WriteJSON(value any) error
}

type Server struct {
	transcriber stt.Transcriber
	handlers    Handlers
	upgrader    websocket.Upgrader

	mu          sync.Mutex
	connections map[*websocket.Conn]struct{}
	closing     bool
	active      sync.WaitGroup
}

// NewServer creates the realtime WebSocket server.
func NewServer(transcriber stt.Transcriber, handlers Handlers) *Server {
	return &Server{
		transcriber: transcriber,
		handlers:    handlers,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     handlers.CheckOrigin,
		},
		connections: make(map[*websocket.Conn]struct{}),
	}
}

// Shutdown closes active audio connections and waits for their handlers to exit.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closing = true
	connections := make([]*websocket.Conn, 0, len(s.connections))
	for conn := range s.connections {
		connections = append(connections, conn)
	}
	s.mu.Unlock()

	for _, conn := range connections {
		_ = conn.Close()
	}

	done := make(chan struct{})
	go func() {
		s.active.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) registerConnection(conn *websocket.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closing {
		return false
	}

	s.connections[conn] = struct{}{}
	s.active.Add(1)
	return true
}

func (s *Server) unregisterConnection(conn *websocket.Conn) {
	s.mu.Lock()
	delete(s.connections, conn)
	s.mu.Unlock()
	s.active.Done()
}

func (s *Server) readMessages(
	ctx context.Context,
	conn *websocket.Conn,
	scope tool.Scope,
	audio chan<- []byte,
) {
	defer close(audio)

	for {
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() == nil &&
				!websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				slog.DebugContext(ctx, "realtime WebSocket reader stopped", "error", err)
			}
			return
		}

		switch messageType {
		case websocket.BinaryMessage:
			select {
			case audio <- data:
			case <-ctx.Done():
				return
			}
		case websocket.TextMessage:
			var message clientMessage
			if err := json.Unmarshal(data, &message); err != nil ||
				message.Type != locationMessageType {
				slog.DebugContext(ctx, "ignored invalid WebSocket message")
				continue
			}
			if s.handlers.Location != nil {
				if err := s.handlers.Location(ctx, scope, message.LocationUpdate); err != nil {
					slog.WarnContext(ctx, "rejected location update", "error", err)
				}
			}
		}
	}
}

func (s *Server) transcribeConnection(
	ctx context.Context,
	scope tool.Scope,
	writer jsonWriter,
	audio <-chan []byte,
) error {
	completed := make(chan string, completedUtteranceBuffer)
	done := make(chan error, 1)

	go func() {
		err := s.transcriber.Transcribe(ctx, audio, completed)
		close(completed)
		done <- err
	}()

	for utterance := range completed {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.handlers.Utterance == nil {
			continue
		}

		response, err := s.handlers.Utterance(ctx, scope, utterance)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.ErrorContext(ctx, "failed to handle utterance", "error", err)
			continue
		}
		response = strings.TrimSpace(response)
		if response == "" {
			continue
		}
		if err := writer.WriteJSON(serverMessage{
			Type: assistantResponseMessageType,
			Text: response,
		}); err != nil {
			return fmt.Errorf("write assistant response: %w", err)
		}
	}

	return <-done
}

// ServeHTTP authenticates and handles a realtime WebSocket connection.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !websocket.IsWebSocketUpgrade(r) {
		w.Header().Set("Upgrade", "websocket")
		http.Error(w, "WebSocket upgrade required", http.StatusUpgradeRequired)
		return
	}
	if s.handlers.Authenticate == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	scope, err := s.handlers.Authenticate(r.URL.Query().Get("ticket"))
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(maxMessageSize)
	if !s.registerConnection(conn) {
		_ = conn.Close()
		return
	}
	defer s.unregisterConnection(conn)
	defer conn.Close()
	if s.handlers.Disconnect != nil {
		defer s.handlers.Disconnect(scope)
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	audio := make(chan []byte, 100)
	go func() {
		s.readMessages(ctx, conn, scope, audio)
		cancel()
	}()

	if err := s.transcribeConnection(ctx, scope, conn, audio); err != nil &&
		!errors.Is(err, context.Canceled) {
		slog.ErrorContext(ctx, "transcription stopped", "error", err)
	}
}
