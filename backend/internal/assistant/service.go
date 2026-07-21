package assistant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

var (
	ErrRouterRequired        = errors.New("assistant router is required")
	ErrAgentRequired         = errors.New("assistant agent is required")
	ErrMemoryRequired        = errors.New("assistant memory reader is required")
	ErrConversationRequired  = errors.New("assistant conversation reader is required")
	ErrTaskConfirmerRequired = errors.New("assistant task confirmer is required")
)

// ActivityRouter decides how the assistant should handle a finalized utterance.
type ActivityRouter interface {
	Route(ctx context.Context, utterance string) (Decision, error)
}

// Agent generates a response for a routed query. Scope must be populated from
// trusted authentication and application-session state by the caller.
type Agent interface {
	Respond(
		ctx context.Context,
		scope tool.Scope,
		query string,
		conversation session.Conversation,
		memories []memory.Card,
	) (string, error)
}

// MemoryReader finds relevant memories within the trusted user scope.
type MemoryReader interface {
	Find(ctx context.Context, scope tool.Scope, lookup memory.Lookup) ([]memory.Card, error)
}

// ConversationReader prepares recent transcript context for the current turn.
type ConversationReader interface {
	Prepare(context.Context, tool.Scope, string, string) (session.Conversation, error)
}

type TaskConfirmer interface {
	Confirm(context.Context, tool.Scope, string) (string, bool, error)
}

// Outcome describes what the assistant decided and any response it generated.
type Outcome struct {
	Decision Decision
	Response string
}

// Service coordinates routing and response generation.
type Service struct {
	router       ActivityRouter
	agent        Agent
	memories     MemoryReader
	conversation ConversationReader
	tasks        TaskConfirmer
}

func NewService(
	activityRouter ActivityRouter,
	agent Agent,
	memories MemoryReader,
	conversation ConversationReader,
	tasks TaskConfirmer,
) (*Service, error) {
	if activityRouter == nil {
		return nil, ErrRouterRequired
	}
	if agent == nil {
		return nil, ErrAgentRequired
	}
	if memories == nil {
		return nil, ErrMemoryRequired
	}
	if conversation == nil {
		return nil, ErrConversationRequired
	}
	if tasks == nil {
		return nil, ErrTaskConfirmerRequired
	}

	return &Service{
		router:       activityRouter,
		agent:        agent,
		memories:     memories,
		conversation: conversation,
		tasks:        tasks,
	}, nil
}

// HandleUtterance resolves clear task confirmations, then routes new speech.
func (s *Service) HandleUtterance(
	ctx context.Context,
	scope tool.Scope,
	utteranceID string,
	utterance string,
) (Outcome, error) {
	turnScope := scope
	turnScope.UtteranceID = strings.TrimSpace(utteranceID)

	confirmation, handled, err := s.tasks.Confirm(ctx, turnScope, utterance)
	if err != nil {
		return Outcome{}, fmt.Errorf("confirm task proposal: %w", err)
	}
	if handled {
		return Outcome{
			Decision: Decision{Action: ActionResolveTask},
			Response: strings.TrimSpace(confirmation),
		}, nil
	}

	decision, err := s.router.Route(ctx, utterance)
	if err != nil {
		return Outcome{}, fmt.Errorf("route utterance: %w", err)
	}

	outcome := Outcome{Decision: decision}
	if decision.Action != ActionRespond && decision.Action != ActionProposeTask {
		return outcome, nil
	}

	query := strings.TrimSpace(decision.Query)
	if query == "" {
		query = strings.TrimSpace(utterance)
	}

	cards, err := s.memories.Find(ctx, scope, decision.MemoryLookup)
	if err != nil {
		slog.WarnContext(ctx, "memory lookup failed", "error", err)
		cards = nil
	}
	conversation, err := s.conversation.Prepare(ctx, scope, utteranceID, query)
	if err != nil {
		slog.WarnContext(ctx, "conversation context failed", "error", err)
	}

	response, err := s.agent.Respond(ctx, turnScope, query, conversation, cards)
	if err != nil {
		return outcome, fmt.Errorf("generate assistant response: %w", err)
	}
	outcome.Response = strings.TrimSpace(response)

	return outcome, nil
}
