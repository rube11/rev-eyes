package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/rube11/rev-eyes/backend/internal/assistant"
	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/realtime"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const (
	memoryAcknowledgment           = "Got it, I'll remember that."
	memoryCorrectionAcknowledgment = "Updated. I'll remember that."
	noMemoryAcknowledgment         = "I couldn't find a reusable fact to remember."
	unsafeMemoryAcknowledgment     = "I can't store passwords, security codes, or financial credentials."
)

type utteranceService interface {
	HandleUtterance(context.Context, tool.Scope, string, string) (assistant.Outcome, error)
}

type transcriptStore interface {
	Append(context.Context, tool.Scope, session.Speaker, string) (string, error)
}

type memoryService interface {
	Capture(tool.Scope, string, string) bool
	RememberExplicit(context.Context, tool.Scope, string, string) error
}

func handleUtterance(
	ctx context.Context,
	scope tool.Scope,
	utterance string,
	service utteranceService,
	transcripts transcriptStore,
	memories memoryService,
) (realtime.UtteranceResult, error) {
	result := realtime.UtteranceResult{}
	utteranceID, err := transcripts.Append(ctx, scope, session.SpeakerUser, utterance)
	if err != nil {
		return result, fmt.Errorf("persist user utterance: %w", err)
	}
	result.WorkspaceResources = []realtime.WorkspaceResource{
		realtime.WorkspaceConversations,
	}

	outcome, err := service.HandleUtterance(ctx, scope, utteranceID, utterance)
	if shouldCaptureMemory(outcome.Decision.Action) {
		if memories == nil || !memories.Capture(scope, utteranceID, utterance) {
			slog.WarnContext(ctx, "memory learning queue unavailable")
		}
	}
	switch outcome.Decision.Action {
	case assistant.ActionProposeTask:
		if outcome.ProposalCreated {
			result.WorkspaceResources = append(
				result.WorkspaceResources,
				realtime.WorkspaceTasks,
			)
		}
	case assistant.ActionProposeWatch:
		if outcome.ProposalCreated {
			result.WorkspaceResources = append(
				result.WorkspaceResources,
				realtime.WorkspaceWatches,
			)
		}
	case assistant.ActionResolveProposal:
		result.WorkspaceResources = append(
			result.WorkspaceResources,
			realtime.WorkspaceTasks,
			realtime.WorkspaceWatches,
		)
	case assistant.ActionMemoryForget, assistant.ActionProfileInclude, assistant.ActionProfileExclude:
		if outcome.MemoryChanged {
			result.WorkspaceResources = append(
				result.WorkspaceResources,
				realtime.WorkspaceMemories,
			)
		}
	}
	if err != nil {
		return result, err
	}

	response := outcome.Response
	if outcome.Decision.Action == assistant.ActionRemember ||
		(outcome.Decision.Action == assistant.ActionMemoryCorrect && response == "") {
		if memories == nil {
			return result, errors.New("memory service is required")
		}
		rememberErr := memories.RememberExplicit(ctx, scope, utteranceID, utterance)
		if errors.Is(rememberErr, memory.ErrUnsafeMemory) {
			response = unsafeMemoryAcknowledgment
		} else if errors.Is(rememberErr, memory.ErrNoMemoryCandidates) {
			response = noMemoryAcknowledgment
		} else if rememberErr != nil {
			return result, fmt.Errorf("persist memory: %w", rememberErr)
		} else {
			result.WorkspaceResources = append(result.WorkspaceResources, realtime.WorkspaceMemories)
			response = memoryAcknowledgment
			if outcome.Decision.Action == assistant.ActionMemoryCorrect {
				response = memoryCorrectionAcknowledgment
			}
		}
	}

	if scope.AlwaysRespond && strings.TrimSpace(response) == "" {
		response = "I couldn’t generate a reply. Please try again."
	}
	if response != "" {
		if _, err := transcripts.Append(
			ctx,
			scope,
			session.SpeakerAssistant,
			response,
		); err != nil {
			return result, fmt.Errorf("persist assistant utterance: %w", err)
		}
	}

	slog.InfoContext(ctx, "utterance handled",
		"action", outcome.Decision.Action,
		"responded", response != "",
	)
	result.Text = response
	result.AwaitingConfirmation = outcome.ProposalCreated
	return result, nil
}

func shouldCaptureMemory(action assistant.Action) bool {
	switch action {
	case assistant.ActionRemember,
		assistant.ActionMemoryReview,
		assistant.ActionMemoryCorrect,
		assistant.ActionMemoryForget,
		assistant.ActionProfileInclude,
		assistant.ActionProfileExclude:
		return false
	default:
		return true
	}
}
