package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const maxMessageSize = 1 << 20

const (
	locationMessageType       = "location"
	listeningStartMessageType = "listening_start"
	listeningStopMessageType  = "listening_stop"

	assistantDoneMessageType     = "assistant_done"
	assistantRepeatMessageType   = "assistant_repeat"
	assistantResponseMessageType = "assistant_response"
	assistantThinkingMessageType = "assistant_thinking"
	listeningStoppedMessageType  = "listening_stopped"
	notificationMessageType      = "notification"
	notificationAckMessageType   = "notification_ack"
	userTranscriptMessageType    = "user_transcript"
	workspaceChangedMessageType  = "workspace_changed"
)

type WorkspaceResource string

const (
	WorkspaceConversations WorkspaceResource = "conversations"
	WorkspaceMemories      WorkspaceResource = "memories"
	WorkspaceWatches       WorkspaceResource = "watches"
	WorkspaceTasks         WorkspaceResource = "tasks"
)

type Authenticator func(ticket string) (tool.Scope, error)
type UtteranceResult struct {
	Text                 string
	AwaitingConfirmation bool
	WorkspaceResources   []WorkspaceResource
}

type UtteranceHandler func(
	ctx context.Context,
	scope tool.Scope,
	utterance string,
) (UtteranceResult, error)
type LocationHandler func(ctx context.Context, scope tool.Scope, update LocationUpdate) error
type NotificationAckHandler func(ctx context.Context, scope tool.Scope, notificationID string) error
type CandidateAudioHandler func(
	ctx context.Context,
	audio []byte,
	format stt.AudioFormat,
) (string, error)

type Handlers struct {
	Authenticate           Authenticator
	CandidateAudio         CandidateAudioHandler
	CandidateMaxConcurrent int
	ClientDiagnostic       ClientDiagnosticHandler
	CheckOrigin            func(r *http.Request) bool
	Connect                func(ctx context.Context, scope tool.Scope) error
	Utterance              UtteranceHandler
	Location               LocationHandler
	NotificationAck        NotificationAckHandler
	Disconnect             func(scope tool.Scope)
}

type LocationUpdate struct {
	Latitude       float64 `json:"latitude"`
	Longitude      float64 `json:"longitude"`
	AccuracyMeters float64 `json:"accuracy_meters,omitempty"`
}

type clientMessage struct {
	Type              string           `json:"type"`
	ID                string           `json:"id,omitempty"`
	Encoding          string           `json:"encoding,omitempty"`
	SampleRate        int              `json:"sample_rate,omitempty"`
	Channels          int              `json:"channels,omitempty"`
	ByteLength        int              `json:"byte_length,omitempty"`
	StartSampleOffset int64            `json:"start_sample_offset,omitempty"`
	EndSampleOffset   int64            `json:"end_sample_offset,omitempty"`
	GateCategory      string           `json:"gate_category,omitempty"`
	GateConfidence    float64          `json:"gate_confidence,omitempty"`
	Diagnostic        ClientDiagnostic `json:"diagnostic,omitempty"`
	LocationUpdate
}

func (message clientMessage) candidateHeader() candidateAudioHeader {
	return candidateAudioHeader{
		ID:                message.ID,
		Encoding:          message.Encoding,
		SampleRate:        message.SampleRate,
		Channels:          message.Channels,
		ByteLength:        message.ByteLength,
		StartSampleOffset: message.StartSampleOffset,
		EndSampleOffset:   message.EndSampleOffset,
		GateCategory:      message.GateCategory,
		GateConfidence:    message.GateConfidence,
	}
}

type serverMessage struct {
	Type                 string              `json:"type"`
	ID                   string              `json:"id,omitempty"`
	Text                 string              `json:"text,omitempty"`
	Error                string              `json:"error,omitempty"`
	AwaitingConfirmation bool                `json:"awaiting_confirmation,omitempty"`
	Resources            []WorkspaceResource `json:"resources,omitempty"`
}

type jsonWriter interface {
	WriteJSON(value any) error
}

type incomingMessage struct {
	messageType int
	data        []byte
}

type Server struct {
	transcriber stt.Transcriber
	handlers    Handlers
	turns       *turnCoordinator
	upgrader    websocket.Upgrader
	hub         *Hub
}

// NewServer creates the realtime WebSocket server.
func NewServer(transcriber stt.Transcriber, handlers Handlers) *Server {
	return NewServerWithHub(transcriber, NewHub(), handlers)
}

// NewServerWithHub creates a realtime server with shared outbound delivery.
func NewServerWithHub(transcriber stt.Transcriber, hub *Hub, handlers Handlers) *Server {
	if hub == nil {
		hub = NewHub()
	}
	return &Server{
		transcriber: transcriber,
		handlers:    handlers,
		hub:         hub,
		turns:       newTurnCoordinator(),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     handlers.CheckOrigin,
		},
	}
}

// Shutdown closes active realtime connections and waits for their handlers to exit.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.hub.Shutdown(ctx)
}

func (s *Server) readMessages(
	ctx context.Context,
	conn *connection,
	messages chan<- incomingMessage,
) {
	defer close(messages)

	for {
		messageType, data, err := conn.socket.ReadMessage()
		if err != nil {
			if ctx.Err() == nil &&
				!websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				slog.DebugContext(ctx, "realtime WebSocket reader stopped", "error", err)
			}
			return
		}

		if messageType != websocket.BinaryMessage && messageType != websocket.TextMessage {
			continue
		}
		select {
		case messages <- incomingMessage{messageType: messageType, data: data}:
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) serveConnection(
	parent context.Context,
	conn *connection,
	scope tool.Scope,
) error {
	ctx, cancel := context.WithCancel(parent)
	messages := make(chan incomingMessage)
	go s.readMessages(ctx, conn, messages)
	var audio chan []byte
	var transcription <-chan error
	defer func() {
		cancel()
		if transcription != nil {
			<-transcription
		}
	}()

	finishTranscription := func(transcriptionErr error) error {
		audio = nil
		transcription = nil

		message := serverMessage{Type: listeningStoppedMessageType}
		if transcriptionErr != nil && !errors.Is(transcriptionErr, context.Canceled) {
			slog.ErrorContext(ctx, "transcription stopped", "error", transcriptionErr)
			message.Error = "Transcription unavailable"
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := conn.WriteJSON(message); err != nil {
			return fmt.Errorf("write listening state: %w", err)
		}
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case transcriptionErr := <-transcription:
			if err := finishTranscription(transcriptionErr); err != nil {
				return err
			}

		case incoming, ok := <-messages:
			if !ok {
				return nil
			}

			if incoming.messageType == websocket.BinaryMessage {
				if audio == nil {
					clear(incoming.data)
					continue
				}
				select {
				case audio <- incoming.data:
				case transcriptionErr := <-transcription:
					if err := finishTranscription(transcriptionErr); err != nil {
						return err
					}
				case <-ctx.Done():
					return ctx.Err()
				}
				continue
			}

			var message clientMessage
			if err := json.Unmarshal(incoming.data, &message); err != nil {
				slog.DebugContext(ctx, "ignored invalid WebSocket message")
				continue
			}

			switch message.Type {
			case locationMessageType:
				if s.handlers.Location != nil {
					if err := s.handlers.Location(ctx, scope, message.LocationUpdate); err != nil {
						slog.WarnContext(ctx, "rejected location update", "error", err)
					}
				}

			case notificationAckMessageType:
				if s.handlers.NotificationAck == nil {
					continue
				}
				if err := s.handlers.NotificationAck(ctx, scope, message.ID); err != nil {
					slog.WarnContext(
						ctx,
						"rejected notification acknowledgement",
						"notification_id",
						message.ID,
						"error",
						err,
					)
				}

			case listeningStartMessageType:
				if transcription != nil {
					continue
				}
				audio = make(chan []byte, 100)
				done := make(chan error, 1)
				transcription = done
				go func(audio <-chan []byte) {
					done <- s.transcribeConnection(ctx, scope, conn, audio)
				}(audio)

			case listeningStopMessageType:
				if transcription == nil {
					if err := conn.WriteJSON(serverMessage{Type: listeningStoppedMessageType}); err != nil {
						return fmt.Errorf("write listening state: %w", err)
					}
					continue
				}
				if audio != nil {
					close(audio)
					audio = nil
				}

			default:
				slog.DebugContext(ctx, "ignored unknown WebSocket message", "type", message.Type)
			}
		}
	}
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
	client := &connection{socket: conn, userID: strings.TrimSpace(scope.UserID)}
	if !s.hub.register(client) {
		_ = client.socket.Close()
		return
	}
	defer s.hub.unregister(client)
	defer client.socket.Close()
	if s.handlers.Disconnect != nil {
		defer s.handlers.Disconnect(scope)
	}
	if s.handlers.Connect != nil {
		if err := s.handlers.Connect(r.Context(), scope); err != nil {
			slog.WarnContext(r.Context(), "realtime connection setup failed", "error", err)
		}
	}

	if err := s.serveConnection(r.Context(), client, scope); err != nil &&
		!errors.Is(err, context.Canceled) {
		slog.DebugContext(r.Context(), "realtime connection stopped", "error", err)
	}
}
