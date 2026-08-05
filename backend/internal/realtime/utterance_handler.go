package realtime

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type utteranceDelivery struct {
	messageID        string
	announceThinking bool
}

func (s *Server) transcribeConnection(
	ctx context.Context,
	scope tool.Scope,
	writer jsonWriter,
	audio <-chan []byte,
) error {
	transcriptionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	completed := make(chan string, completedUtteranceBuffer)
	done := make(chan error, 1)

	go func() {
		err := s.transcriber.Transcribe(
			transcriptionCtx,
			audio,
			completed,
			func(transcript string) error {
				transcript = strings.TrimSpace(transcript)
				if transcript == "" {
					return nil
				}
				if err := writer.WriteJSON(serverMessage{
					Type: userTranscriptMessageType,
					Text: transcript,
				}); err != nil {
					return fmt.Errorf("write user transcript: %w", err)
				}
				return nil
			},
		)
		close(completed)
		done <- err
	}()

	for utterance := range completed {
		if err := s.handleCompletedUtterance(
			transcriptionCtx,
			scope,
			writer,
			utterance,
			utteranceDelivery{announceThinking: true},
		); err != nil {
			return err
		}
	}

	return <-done
}

func (s *Server) handleCompletedUtterance(
	ctx context.Context,
	scope tool.Scope,
	writer jsonWriter,
	utterance string,
	delivery utteranceDelivery,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if isRepeatRequest(utterance) {
		return writer.WriteJSON(serverMessage{
			Type: assistantRepeatMessageType,
			ID:   delivery.messageID,
		})
	}
	if s.handlers.Utterance == nil {
		return writer.WriteJSON(serverMessage{
			Type: assistantDoneMessageType,
			ID:   delivery.messageID,
		})
	}
	releaseTurn, err := s.turns.acquire(ctx, scope)
	if err != nil {
		return err
	}
	defer releaseTurn()
	if err := ctx.Err(); err != nil {
		return err
	}
	if delivery.announceThinking {
		if err := writer.WriteJSON(serverMessage{
			Type: assistantThinkingMessageType,
			ID:   delivery.messageID,
		}); err != nil {
			return fmt.Errorf("write assistant thinking state: %w", err)
		}
	}
	result, err := s.handlers.Utterance(ctx, scope, utterance)
	if len(result.WorkspaceResources) > 0 {
		s.hub.WorkspaceChanged(scope.UserID, result.WorkspaceResources...)
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.ErrorContext(ctx, "failed to handle utterance", "error", err)
		if writeErr := writer.WriteJSON(serverMessage{
			Type: assistantDoneMessageType,
			ID:   delivery.messageID,
		}); writeErr != nil {
			return fmt.Errorf("write assistant done state: %w", writeErr)
		}
		return nil
	}
	response := strings.TrimSpace(result.Text)
	if response == "" {
		if err := writer.WriteJSON(serverMessage{
			Type: assistantDoneMessageType,
			ID:   delivery.messageID,
		}); err != nil {
			return fmt.Errorf("write assistant done state: %w", err)
		}
		return nil
	}
	if err := writer.WriteJSON(serverMessage{
		Type:                 assistantResponseMessageType,
		ID:                   delivery.messageID,
		Text:                 response,
		AwaitingConfirmation: result.AwaitingConfirmation,
	}); err != nil {
		return fmt.Errorf("write assistant response: %w", err)
	}
	return nil
}
