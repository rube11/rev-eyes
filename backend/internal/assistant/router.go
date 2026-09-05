package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
)

type Action string

const (
	ActionIgnore          Action = "ignore"
	ActionRespond         Action = "respond"
	ActionStateUpdate     Action = "state_update"
	ActionStateTransition Action = "state_transition"
	ActionRemember        Action = "remember"
	ActionMemoryReview    Action = "memory_review"
	ActionMemoryCorrect   Action = "memory_correct"
	ActionMemoryForget    Action = "memory_forget"
	ActionProfileInclude  Action = "profile_include"
	ActionProfileExclude  Action = "profile_exclude"
	ActionProposeTask     Action = "propose_task"
	ActionProposeWatch    Action = "propose_watch"
	ActionResolveProposal Action = "resolve_proposal"
)

type Decision struct {
	Action          Action        `json:"action"`
	Query           string        `json:"query"`
	MemoryLookup    memory.Lookup `json:"memory_lookup"`
	MemoryReviewAll bool          `json:"memory_review_all"`
}

type Router struct {
	classify func(ctx context.Context, utterance string) (string, error)
}

func NewRouter(classify func(ctx context.Context, utterance string) (string, error)) *Router {
	return &Router{classify: classify}
}

func (r *Router) Route(ctx context.Context, utterance string) (Decision, error) {
	return r.route(ctx, utterance, utterance)
}

// RouteWithContext resolves the latest turn using bounded prior dialogue, not
// another model call. Rule-based audio ignores still inspect only new speech.
func (r *Router) RouteWithContext(ctx context.Context, utterance string, conversation session.Conversation) (Decision, error) {
	messages := conversation.Messages
	if len(messages) > 6 {
		messages = messages[len(messages)-6:]
	}
	if len(messages) == 0 {
		return r.Route(ctx, utterance)
	}
	type turn struct {
		Speaker session.Speaker `json:"speaker"`
		Text    string          `json:"text"`
	}
	recent := make([]turn, 0, len(messages))
	for _, message := range messages {
		runes := []rune(message.Text)
		if len(runes) > 800 {
			runes = runes[:800]
		}
		recent = append(recent, turn{message.Speaker, string(runes)})
	}
	input, err := json.Marshal(struct {
		Recent []turn `json:"recent_dialogue"`
		Latest string `json:"latest_utterance"`
	}{recent, utterance})
	if err != nil {
		return Decision{}, err
	}
	return r.route(ctx, utterance, string(input))
}

func (r *Router) route(ctx context.Context, utterance, classifierInput string) (Decision, error) {
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

	response, err := r.classify(ctx, classifierInput)
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
	if decision.Action == ActionMemoryReview && decision.MemoryReviewAll {
		decision.Query = ""
		decision.MemoryLookup = memory.Lookup{}
	} else {
		decision.MemoryReviewAll = false
	}
	if decision.Action == ActionRespond ||
		decision.Action == ActionStateTransition ||
		decision.Action == ActionMemoryReview ||
		decision.Action == ActionMemoryForget ||
		decision.Action == ActionProfileInclude ||
		decision.Action == ActionProfileExclude ||
		decision.Action == ActionProposeTask ||
		decision.Action == ActionProposeWatch {
		decision.MemoryLookup = decision.MemoryLookup.Normalize()
	} else {
		decision.MemoryLookup = memory.Lookup{}
	}
	slog.InfoContext(ctx, "router decision", "action", decision.Action)

	return decision, nil
}

func validateDecision(decision Decision) Decision {
	switch decision.Action {
	case ActionIgnore,
		ActionRespond,
		ActionStateUpdate,
		ActionStateTransition,
		ActionRemember,
		ActionMemoryReview,
		ActionMemoryCorrect,
		ActionMemoryForget,
		ActionProfileInclude,
		ActionProfileExclude,
		ActionProposeTask,
		ActionProposeWatch:
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
