package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/rube11/rev-eyes/backend/internal/memory"
)

type Action string

const (
	ActionIgnore          Action = "ignore"
	ActionRespond         Action = "respond"
	ActionStateUpdate     Action = "state_update"
	ActionRemember        Action = "remember"
	ActionProposeTask     Action = "propose_task"
	ActionProposeWatch    Action = "propose_watch"
	ActionResolveProposal Action = "resolve_proposal"
)

type Decision struct {
	Action       Action        `json:"action"`
	Query        string        `json:"query"`
	MemoryLookup memory.Lookup `json:"memory_lookup"`
	Memory       *memory.Card  `json:"memory"`
}

type Router struct {
	classify func(ctx context.Context, utterance string) (string, error)
}

func NewRouter(classify func(ctx context.Context, utterance string) (string, error)) *Router {
	return &Router{classify: classify}
}

func (r *Router) Route(ctx context.Context, utterance string) (Decision, error) {
	fallback := Decision{Action: ActionIgnore}
	utterance = strings.TrimSpace(utterance)

	if shouldIgnore(utterance) {
		slog.InfoContext(ctx, "router ignored utterance by rule")
		return fallback, nil
	}

	slog.InfoContext(ctx, "router classifying utterance")

	if r == nil || r.classify == nil {
		err := errors.New("router classifier is required")
		slog.ErrorContext(ctx, "router classification failed", "error", err)
		return fallback, err
	}

	response, err := r.classify(ctx, utterance)
	if err != nil {
		err = fmt.Errorf("classify utterance: %w", err)
		slog.ErrorContext(ctx, "router classification failed", "error", err)
		return fallback, err
	}

	var decision Decision
	if err := json.Unmarshal([]byte(response), &decision); err != nil {
		err = fmt.Errorf("decode router decision: %w", err)
		slog.ErrorContext(ctx, "router response decoding failed", "error", err)
		return fallback, err
	}

	decision.Query = strings.TrimSpace(decision.Query)
	decision = validateDecision(decision)
	if decision.Action == ActionRespond ||
		decision.Action == ActionProposeTask ||
		decision.Action == ActionProposeWatch {
		decision.MemoryLookup = decision.MemoryLookup.Normalize()
	} else {
		decision.MemoryLookup = memory.Lookup{}
	}
	if decision.Action != ActionRemember {
		decision.Memory = nil
	} else if decision.Memory == nil {
		err := fmt.Errorf("%w: remember decision has no card", memory.ErrCardInvalid)
		slog.ErrorContext(ctx, "router memory validation failed", "error", err)
		return fallback, err
	} else {
		card := decision.Memory.Normalize()
		if err := card.Validate(); err != nil {
			slog.ErrorContext(ctx, "router memory validation failed", "error", err)
			return fallback, err
		}
		decision.Memory = &card
	}
	slog.InfoContext(ctx, "router decision", "action", decision.Action)

	return decision, nil
}

func validateDecision(decision Decision) Decision {
	switch decision.Action {
	case ActionIgnore, ActionRespond, ActionStateUpdate, ActionRemember, ActionProposeTask, ActionProposeWatch:
		return decision
	default:
		return Decision{Action: ActionIgnore}
	}
}

func shouldIgnore(utterance string) bool {
	normalized := strings.ToLower(strings.TrimSpace(utterance))
	normalized = strings.Trim(normalized, ".,!?;:")

	if normalized == "" {
		return true
	}

	switch normalized {
	case "um", "uh", "hmm", "mm", "mhm":
		return true
	default:
		return false
	}
}
