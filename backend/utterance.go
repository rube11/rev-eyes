package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/rube11/rev-eyes/backend/internal/assistant"
	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/realtime"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const memoryAcknowledgment = "Got it, I'll remember that."

type utteranceService interface {
	HandleUtterance(context.Context, tool.Scope, string, string) (assistant.Outcome, error)
}

type transcriptStore interface {
	Append(context.Context, tool.Scope, session.Speaker, string) (string, error)
}

type memoryStore interface {
	Remember(context.Context, tool.Scope, string, memory.Card) error
}

func handleUtterance(
	ctx context.Context,
	scope tool.Scope,
	utterance string,
	service utteranceService,
	transcripts transcriptStore,
	memories memoryStore,
) (realtime.UtteranceResult, error) {
	utteranceID, err := transcripts.Append(ctx, scope, session.SpeakerUser, utterance)
	if err != nil {
		return realtime.UtteranceResult{}, fmt.Errorf("persist user utterance: %w", err)
	}

	outcome, err := service.HandleUtterance(ctx, scope, utteranceID, utterance)
	if err != nil {
		return realtime.UtteranceResult{}, err
	}

	response := outcome.Response
	if outcome.Decision.Action == assistant.ActionRemember {
		if outcome.Decision.Memory == nil {
			return realtime.UtteranceResult{}, errors.New("remember decision has no memory card")
		}
		if err := memories.Remember(
			ctx,
			scope,
			utteranceID,
			*outcome.Decision.Memory,
		); err != nil {
			return realtime.UtteranceResult{}, fmt.Errorf("persist memory: %w", err)
		}
		response = memoryAcknowledgment
	}

	if response != "" {
		if _, err := transcripts.Append(
			ctx,
			scope,
			session.SpeakerAssistant,
			response,
		); err != nil {
			return realtime.UtteranceResult{}, fmt.Errorf("persist assistant utterance: %w", err)
		}
	}

	slog.InfoContext(ctx, "utterance handled",
		"action", outcome.Decision.Action,
		"responded", response != "",
	)
	return realtime.UtteranceResult{
		Text: response,
		AwaitingConfirmation: outcome.Decision.Action == assistant.ActionProposeTask ||
			outcome.Decision.Action == assistant.ActionProposeWatch,
	}, nil
}
