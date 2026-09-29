package realtime

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/candidate"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

// runConversation owns one paid connection across multiple assistant turns.
// The idle deadline lives on the server, independent of phone timers.
func (s *Server) runConversation(ctx context.Context, scope tool.Scope, writer jsonWriter, audio <-chan stt.AudioInput, automatic bool) error {
	return s.runConversationWithLogger(ctx, scope, writer, audio, automatic, slog.Default())
}

func (s *Server) runConversationWithLogger(ctx context.Context, scope tool.Scope, writer jsonWriter, audio <-chan stt.AudioInput, automatic bool, logger *slog.Logger) error {
	started := time.Now()
	endReason := "unavailable"
	var transcriptUpdates, finalized, accepted, rejectedWake, acceptedContextOnly atomic.Int64
	var firstTranscript, firstFinalized atomic.Int64
	firstTranscript.Store(-1)
	firstFinalized.Store(-1)
	// Registered before the worker join below, so the single summary includes
	// the completed stream. Never log speech, PCM, account IDs, or raw errors.
	defer func() {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			endReason = "context_deadline"
		} else if ctx.Err() != nil {
			endReason = "context_canceled"
		}
		logger.InfoContext(ctx, "conversation summary",
			"automatic", automatic,
			"duration_ms", time.Since(started).Milliseconds(),
			"first_transcript_ms", firstTranscript.Load(),
			"first_finalized_ms", firstFinalized.Load(),
			"transcript_updates", transcriptUpdates.Load(),
			"finalized_utterances", finalized.Load(),
			"accepted_utterances", accepted.Load(),
			"rejected_wake", rejectedWake.Load(),
			"accepted_context_only", acceptedContextOnly.Load(),
			"end_reason", endReason,
		)
	}()
	transcriber := s.handlers.ConversationTranscriber
	if transcriber == nil {
		return errors.New("conversation streaming unavailable")
	}
	if s.capacity.paidWorkPermits != nil {
		select {
		case s.capacity.paidWorkPermits <- struct{}{}:
			defer func() { <-s.capacity.paidWorkPermits }()
		default:
			endReason = "capacity_exhausted"
			return errors.New("conversation capacity exhausted")
		}
	}
	if err := writer.WriteJSON(serverMessage{Type: "conversation_started"}); err != nil {
		endReason = "start_delivery_failed"
		return err
	}
	idle := s.conversationIdle
	if idle <= 0 {
		idle = 30 * time.Second
	}
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	activity := make(chan struct{}, 1)
	var lastActivity atomic.Int64
	var busy atomic.Bool
	touch := func() {
		lastActivity.Store(time.Now().UnixNano())
		select {
		case activity <- struct{}{}:
		default:
		}
	}
	touch()
	completed := make(chan stt.Utterance, completedUtteranceBuffer)
	sttDone := make(chan error, 1)
	workerDone := make(chan error, 1)
	go func() {
		err := transcriber.TranscribeConversation(streamCtx, audio, completed, func(text string) error {
			if strings.TrimSpace(text) == "" {
				return nil
			}
			transcriptUpdates.Add(1)
			firstTranscript.CompareAndSwap(-1, time.Since(started).Milliseconds())
			touch()
			return writer.WriteJSON(serverMessage{Type: userTranscriptMessageType, Text: text})
		})
		close(completed)
		sttDone <- err
	}()
	go func() {
		authorized := !automatic
		for text := range completed {
			finalized.Add(1)
			firstFinalized.CompareAndSwap(-1, time.Since(started).Milliseconds())
			if streamCtx.Err() != nil {
				workerDone <- streamCtx.Err()
				return
			}
			if !authorized {
				if _, matched := candidate.MatchWakePhrase(text.Text); !matched {
					rejectedWake.Add(1)
					continue
				}
				authorized = true
			}
			accepted.Add(1)
			if text.ContextOnly() {
				acceptedContextOnly.Add(1)
			}
			busy.Store(true)
			touch()
			turnCtx, stop := context.WithTimeout(streamCtx, defaultCandidateProcessingTimeout)
			turnScope := scope
			turnScope.Speech = &text
			err := s.handleCompletedUtterance(turnCtx, turnScope, writer, text.Text)
			stop()
			touch()
			busy.Store(false)
			if err != nil {
				workerDone <- err
				return
			}
		}
		workerDone <- nil
	}()
	// Join both goroutines before releasing stream admission or returning to
	// keyword listening. Neither can deliver an old turn into a new conversation.
	var sttFinished, workerFinished bool
	defer func() {
		cancel()
		if !sttFinished {
			<-sttDone
		}
		if !workerFinished {
			<-workerDone
		}
	}()
	timer := time.NewTimer(idle)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-sttDone:
			endReason = "transcriber_stopped"
			sttFinished = true
			if err == nil {
				err = errors.New("Deepgram conversation disconnected")
			}
			return err
		case err := <-workerDone:
			endReason = "turn_worker_stopped"
			workerFinished = true
			if err == nil {
				err = errors.New("conversation ended unexpectedly")
			}
			return err
		case <-activity:
			timer.Reset(idle)
		case <-timer.C:
			remaining := idle - time.Since(time.Unix(0, lastActivity.Load()))
			if busy.Load() {
				timer.Reset(idle)
				continue
			}
			if remaining > 0 {
				timer.Reset(remaining)
				continue
			}
			cancel()
			// Drain/join before announcing idle to preserve message ordering.
			<-sttDone
			sttFinished = true
			<-workerDone
			workerFinished = true
			endReason = "idle"
			if err := writer.WriteJSON(serverMessage{Type: "conversation_idle"}); err != nil {
				endReason = "idle_delivery_failed"
				return err
			}
			return nil
		}
	}
}
