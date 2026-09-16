package realtime

import (
	"context"
	"errors"
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
	transcriber := s.handlers.ConversationTranscriber
	if transcriber == nil {
		return errors.New("conversation streaming unavailable")
	}
	if s.capacity.paid != nil {
		select {
		case s.capacity.paid <- struct{}{}:
			defer func() { <-s.capacity.paid }()
		default:
			return errors.New("conversation capacity exhausted")
		}
	}
	if err := writer.WriteJSON(serverMessage{Type: "conversation_started"}); err != nil {
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
	completed := make(chan string, completedUtteranceBuffer)
	sttDone := make(chan error, 1)
	workerDone := make(chan error, 1)
	go func() {
		err := transcriber.TranscribeConversation(streamCtx, audio, completed, func(text string) error {
			if strings.TrimSpace(text) == "" {
				return nil
			}
			touch()
			return writer.WriteJSON(serverMessage{Type: userTranscriptMessageType, Text: text})
		})
		close(completed)
		sttDone <- err
	}()
	go func() {
		authorized := !automatic
		for text := range completed {
			if streamCtx.Err() != nil {
				workerDone <- streamCtx.Err()
				return
			}
			if !authorized {
				if _, matched := candidate.MatchWakePhrase(text); !matched {
					continue
				}
				authorized = true
			}
			busy.Store(true)
			touch()
			turnCtx, stop := context.WithTimeout(streamCtx, defaultCandidateProcessingTimeout)
			err := s.handleCompletedUtterance(turnCtx, scope, writer, text)
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
			sttFinished = true
			if err == nil {
				err = errors.New("Deepgram conversation disconnected")
			}
			return err
		case err := <-workerDone:
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
			return writer.WriteJSON(serverMessage{Type: "conversation_idle"})
		}
	}
}
