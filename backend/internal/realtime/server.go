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
	"github.com/rube11/rev-eyes/backend/internal/ambient"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const (
	maxMessageSize              = 1 << 20
	defaultCandidateConcurrency = 1
	candidateAdmissionFactor    = 2
)

type incomingMessage struct {
	messageType int
	data        []byte
}

// Keep PCM and controls ordered without treating a short native inference
// pause as overload. The bounded queue applies backpressure to the socket
// reader; a stalled worker still closes the connection after two seconds.
func enqueueAmbient(ctx context.Context, input chan<- ambient.Input, event ambient.Input) error {
	select {
	case input <- event:
		return nil
	default:
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case input <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("ambient input stalled for two seconds")
	}
}

type Server struct {
	transcriber      stt.Transcriber
	handlers         Handlers
	capacity         *transcriptionCapacity
	candidateTimeout time.Duration
	conversationIdle time.Duration
	turns            *turnCoordinator
	upgrader         websocket.Upgrader
	hub              *Hub
}

// NewServer creates the realtime WebSocket server.
func NewServer(transcriber stt.Transcriber, handlers Handlers) *Server {
	return NewServerWithHub(transcriber, NewHub(), handlers)
}

// NewServerWithHub creates a realtime server with shared outbound delivery.
func NewServerWithHub(transcriber stt.Transcriber, hub *Hub, handlers Handlers) *Server {
	capacity := &transcriptionCapacity{}
	if handlers.CandidateAudio != nil {
		maxConcurrent := handlers.CandidateMaxConcurrent
		if maxConcurrent <= 0 {
			maxConcurrent = defaultCandidateConcurrency
		}
		capacity.retained = make(chan struct{}, maxConcurrent*candidateAdmissionFactor)
		capacity.paid = make(chan struct{}, maxConcurrent)
	}
	return newServer(transcriber, hub, handlers, capacity)
}

// Main listening and diagnostics share these limits. Retained slots last until
// clip PCM is cleared; paid slots cover transcription AND downstream turns.
type transcriptionCapacity struct {
	retained chan struct{}
	paid     chan struct{}
}

func newServer(transcriber stt.Transcriber, hub *Hub, handlers Handlers, capacity *transcriptionCapacity) *Server {
	if handlers.AmbientStreaming != nil && handlers.ConversationTranscriber == nil {
		panic("streaming ambient listening requires conversation transcription")
	}
	if hub == nil {
		hub = NewHub()
	}
	return &Server{
		transcriber:      transcriber,
		handlers:         handlers,
		capacity:         capacity,
		candidateTimeout: defaultCandidateProcessingTimeout,
		hub:              hub,
		turns:            newTurnCoordinator(),
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
	if s.handlers.Diagnostics {
		cancel()
		ctx, cancel = context.WithTimeout(parent, 7*time.Minute)
	}
	messages := make(chan incomingMessage)
	go s.readMessages(ctx, conn, messages)
	candidateJobs := make(chan candidateJob, 1)
	candidateWorkerDone := make(chan struct{})
	go s.runCandidateWorker(ctx, scope, conn, candidateJobs, candidateWorkerDone)

	var ambientActive bool
	var ambientCancel context.CancelFunc
	var ambientInputs chan ambient.Input
	var audio chan stt.AudioInput
	// This connection starts one audio worker at a time and joins its result.
	transcription := make(chan error, 1)
	transcribing := false
	legacyCapturing := false
	var pendingCandidate *candidateAudioHeader
	audioMode := audioModeUnset
	if s.handlers.Diagnostics {
		audioMode = audioModeAmbient
	}
	usedCandidateIDs := newCandidateIDWindow()
	var diagnosticLimiter clientDiagnosticLimiter
	defer func() {
		cancel()
		if transcribing {
			<-transcription
		}
		close(candidateJobs)
		<-candidateWorkerDone
	}()

	finishTranscription := func(transcriptionErr error) error {
		if ambientCancel != nil {
			ambientCancel()
			ambientCancel = nil
		}
		audio = nil
		legacyCapturing = false
		transcribing = false
		ambientActive = false
		// The reader may have queued a frame concurrently with worker exit.
		for len(ambientInputs) > 0 {
			event := <-ambientInputs
			clear(event.PCM)
		}
		ambientInputs = nil

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
						clear(incoming.data)
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
						clear(incoming.data)
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
						clear(incoming.data)
						slog.WarnContext(ctx, "candidate audio queue full", "candidate_id", header.ID)
						if err := conn.WriteJSON(candidateDoneMessage(header.ID)); err != nil {
							return fmt.Errorf("write candidate queue state: %w", err)
						}
					}
					continue
				}
				if ambientActive {
					if len(incoming.data) > 32000 {
						clear(incoming.data)
						return errors.New("ambient audio frame exceeds one second")
					}
					if err := enqueueAmbient(ctx, ambientInputs, ambient.Input{PCM: incoming.data}); err != nil {
						clear(incoming.data)
						return err
					}
					continue
				}
				if !legacyCapturing {
					clear(incoming.data)
					continue
				}

				select {
				case audio <- stt.AudioInput{PCM: incoming.data}:
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
				if s.handlers.CandidateAudio == nil {
					continue
				}
				header := message.candidateHeader()
				if audioMode == audioModeLegacy || audioMode == audioModeAmbient || transcribing {
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

			case "ambient_start":
				if transcribing {
					continue
				}
				if audioMode == audioModeUnset {
					audioMode = audioModeAmbient
				}
				if s.handlers.Ambient == nil || s.handlers.CandidateAudio == nil || audioMode != audioModeAmbient {
					if err := conn.WriteJSON(serverMessage{Type: listeningStoppedMessageType, Error: "Server listening unavailable"}); err != nil {
						return err
					}
					continue
				}
				audioMode = audioModeAmbient
				ambientActive = true
				ambientInputs = make(chan ambient.Input, 16)
				ambientCtx, stopAmbient := context.WithCancel(ctx)
				ambientCancel = stopAmbient
				transcribing = true
				go func(input <-chan ambient.Input) {
					if s.handlers.AmbientStreaming != nil && !s.handlers.Diagnostics {
						transcription <- s.handlers.AmbientStreaming(ambientCtx, input, func(ctx context.Context, audio <-chan stt.AudioInput, automatic bool) error {
							return s.runConversation(ctx, scope, conn, audio, automatic)
						})
					} else {
						transcription <- s.listenAmbient(ambientCtx, conn, input, candidateJobs)
					}
				}(ambientInputs)

			case "ambient_stop":
				if ambientActive {
					ambientCancel()
					if err := finishTranscription(<-transcription); err != nil {
						return err
					}
				}
			case "ambient_reply_arm", "ambient_reply_disarm", "conversation_finalize", "conversation_stop":
				if ambientActive {
					if err := enqueueAmbient(ctx, ambientInputs, ambient.Input{Control: message.Type}); err != nil {
						return err
					}
				}
			case listeningStartMessageType:
				if ambientActive {
					if err := enqueueAmbient(ctx, ambientInputs, ambient.Input{Control: message.Type}); err != nil {
						return err
					}
					continue
				}
				if audioMode == audioModeAmbient {
					continue
				} // Never fall back to paid streaming.
				if audioMode == audioModeCandidate {
					if err := conn.WriteJSON(serverMessage{
						Type:  listeningStoppedMessageType,
						Error: "Candidate audio mode is active",
					}); err != nil {
						return fmt.Errorf("write incompatible listening state: %w", err)
					}
					continue
				}
				if transcribing {
					continue
				}
				audioMode = audioModeLegacy
				audio = make(chan stt.AudioInput, 100)
				legacyCapturing = true
				transcribing = true
				go func(audio <-chan stt.AudioInput) {
					transcription <- s.transcribeConnection(ctx, scope, conn, audio)
				}(audio)

			case listeningStopMessageType:
				if ambientActive {
					if err := enqueueAmbient(ctx, ambientInputs, ambient.Input{Control: message.Type}); err != nil {
						return err
					}
					continue
				}
				if !transcribing {
					if err := conn.WriteJSON(serverMessage{Type: listeningStoppedMessageType}); err != nil {
						return fmt.Errorf("write listening state: %w", err)
					}
					continue
				}
				if legacyCapturing {
					close(audio)
					legacyCapturing = false
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
