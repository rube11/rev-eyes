package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const (
	completedUtteranceBuffer    = 10
	maxMessageSize              = 1 << 20
	defaultCandidateConcurrency = 1
	candidateAdmissionFactor    = 2
)

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
	transcriber         stt.Transcriber
	handlers            Handlers
	candidateAdmissions chan struct{}
	candidatePermits    chan struct{}
	candidateTimeout    time.Duration
	turns               *turnCoordinator
	upgrader            websocket.Upgrader
	hub                 *Hub
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
	var candidateAdmissions chan struct{}
	var candidatePermits chan struct{}
	if handlers.CandidateAudio != nil {
		maxConcurrent := handlers.CandidateMaxConcurrent
		if maxConcurrent <= 0 {
			maxConcurrent = defaultCandidateConcurrency
		}
		candidateAdmissions = make(
			chan struct{},
			maxConcurrent*candidateAdmissionFactor,
		)
		candidatePermits = make(chan struct{}, maxConcurrent)
	}
	return &Server{
		transcriber:         transcriber,
		handlers:            handlers,
		candidateAdmissions: candidateAdmissions,
		candidatePermits:    candidatePermits,
		candidateTimeout:    defaultCandidateProcessingTimeout,
		hub:                 hub,
		turns:               newTurnCoordinator(),
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
	candidateJobs := make(chan candidateJob, 1)
	candidateWorkerDone := make(chan struct{})
	go s.runCandidateWorker(ctx, scope, conn, candidateJobs, candidateWorkerDone)

	var audio chan []byte
	var transcription <-chan error
	var pendingCandidate *candidateAudioHeader
	audioMode := audioModeUnset
	usedCandidateIDs := newCandidateIDWindow()
	var diagnosticLimiter clientDiagnosticLimiter
	defer func() {
		cancel()
		if transcription != nil {
			<-transcription
		}
		close(candidateJobs)
		<-candidateWorkerDone
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
				if pendingCandidate != nil {
					header := *pendingCandidate
					pendingCandidate = nil
					if len(incoming.data) != header.ByteLength {
						clearCandidateAudio(incoming.data)
						slog.WarnContext(
							ctx,
							"rejected candidate audio payload",
							"candidate_id", header.ID,
							"expected_bytes", header.ByteLength,
							"received_bytes", len(incoming.data),
						)
						if err := conn.WriteJSON(candidateDoneMessage(header.ID)); err != nil {
							return fmt.Errorf("write candidate rejection: %w", err)
						}
						continue
					}
					job := candidateJob{
						header:     header,
						audio:      incoming.data,
						acceptedAt: time.Now(),
					}
					if !s.tryAdmitCandidate(&job) {
						clearCandidateAudio(incoming.data)
						slog.WarnContext(
							ctx,
							"candidate audio capacity full",
							"candidate_id", header.ID,
						)
						if err := conn.WriteJSON(candidateDoneMessage(header.ID)); err != nil {
							return fmt.Errorf("write candidate capacity state: %w", err)
						}
						continue
					}
					select {
					case candidateJobs <- job:
						slog.InfoContext(
							ctx,
							"accepted candidate audio",
							"candidate_id", header.ID,
							"bytes", header.ByteLength,
						)
					default:
						s.releaseCandidateAdmission(job)
						clearCandidateAudio(incoming.data)
						slog.WarnContext(ctx, "candidate audio queue full", "candidate_id", header.ID)
						if err := conn.WriteJSON(candidateDoneMessage(header.ID)); err != nil {
							return fmt.Errorf("write candidate queue state: %w", err)
						}
					}
					continue
				}
				if audio == nil {
					clearCandidateAudio(incoming.data)
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
			case moonshineDiagnosticMessageType:
				if s.handlers.ClientDiagnostic == nil {
					continue
				}
				diagnostic, err := message.Diagnostic.normalized()
				if err != nil {
					slog.DebugContext(ctx, "ignored invalid client diagnostic")
					continue
				}
				if !diagnosticLimiter.allow(time.Now()) {
					continue
				}
				s.handlers.ClientDiagnostic(ctx, diagnostic)

			case candidateAudioMessageType:
				header := message.candidateHeader()
				if audioMode == audioModeLegacy || transcription != nil {
					slog.WarnContext(ctx, "rejected candidate audio during legacy transcription")
					if err := conn.WriteJSON(candidateDoneMessage(message.ID)); err != nil {
						return fmt.Errorf("write candidate mode rejection: %w", err)
					}
					continue
				}
				if pendingCandidate != nil {
					return errors.New("candidate header received before prior payload")
				}
				if err := header.validate(); err != nil {
					slog.WarnContext(ctx, "rejected candidate audio header", "error", err)
					if writeErr := conn.WriteJSON(candidateDoneMessage(message.ID)); writeErr != nil {
						return fmt.Errorf("write candidate header rejection: %w", writeErr)
					}
					continue
				}
				if usedCandidateIDs.Contains(header.ID) {
					slog.WarnContext(ctx, "rejected duplicate candidate id", "candidate_id", header.ID)
					if err := conn.WriteJSON(candidateDoneMessage(header.ID)); err != nil {
						return fmt.Errorf("write duplicate candidate rejection: %w", err)
					}
					continue
				}
				audioMode = audioModeCandidate
				usedCandidateIDs.Add(header.ID)
				pendingCandidate = &header

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
				if audioMode == audioModeCandidate {
					if err := conn.WriteJSON(serverMessage{
						Type:  listeningStoppedMessageType,
						Error: "Candidate audio mode is active",
					}); err != nil {
						return fmt.Errorf("write incompatible listening state: %w", err)
					}
					continue
				}
				if transcription != nil {
					continue
				}
				audioMode = audioModeLegacy
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
