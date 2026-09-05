package assistant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

var (
	ErrRouterRequired            = errors.New("assistant router is required")
	ErrAgentRequired             = errors.New("assistant agent is required")
	ErrMemoryRequired            = errors.New("assistant memory reader is required")
	ErrConversationRequired      = errors.New("assistant conversation reader is required")
	ErrProposalConfirmerRequired = errors.New("assistant proposal confirmer is required")
)

const proposalResponseFallback = "I have that ready. Should I save it?"

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

// AgentResult includes effects that the response text alone cannot prove.
type AgentResult struct {
	Text            string
	ProposalCreated bool
}

// ProposalAwareAgent reports whether a successful tool call created a proposal.
// Agents that do not implement it still work, but never claim confirmation is
// pending based only on the router's intent.
type ProposalAwareAgent interface {
	Agent
	RespondWithResult(
		ctx context.Context,
		scope tool.Scope,
		query string,
		conversation session.Conversation,
		memories []memory.Card,
	) (AgentResult, error)
}

// MemoryManager retrieves and manages memories within the trusted user scope.
type MemoryManager interface {
	Find(ctx context.Context, scope tool.Scope, lookup memory.Lookup) ([]memory.Card, error)
	Review(ctx context.Context, scope tool.Scope, lookup memory.Lookup) ([]memory.Card, error)
	Forget(ctx context.Context, scope tool.Scope, lookup memory.Lookup) (int, error)
}

// ConversationReader prepares recent transcript context for the current turn.
type ConversationReader interface {
	Prepare(context.Context, tool.Scope, string, string) (session.Conversation, error)
}

type ProposalConfirmer interface {
	Confirm(context.Context, tool.Scope, string) (string, bool, error)
}

// Outcome describes what the assistant decided and any response it generated.
type Outcome struct {
	Decision        Decision
	Response        string
	ProposalCreated bool
	MemoryChanged   bool
}

// Service coordinates routing and response generation.
type Service struct {
	router       ActivityRouter
	agent        Agent
	memories     MemoryManager
	conversation ConversationReader
	proposals    ProposalConfirmer
}

func NewService(
	activityRouter ActivityRouter,
	agent Agent,
	memories MemoryManager,
	conversation ConversationReader,
	proposals ProposalConfirmer,
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
	if proposals == nil {
		return nil, ErrProposalConfirmerRequired
	}

	return &Service{
		router:       activityRouter,
		agent:        agent,
		memories:     memories,
		conversation: conversation,
		proposals:    proposals,
	}, nil
}

// HandleUtterance resolves clear proposal confirmations, then routes new speech.
func (s *Service) HandleUtterance(
	ctx context.Context,
	scope tool.Scope,
	utteranceID string,
	utterance string,
) (Outcome, error) {
	turnScope := scope
	turnScope.UtteranceID = strings.TrimSpace(utteranceID)

	confirmation, handled, err := s.proposals.Confirm(ctx, turnScope, utterance)
	if err != nil {
		return Outcome{}, fmt.Errorf("confirm proposal: %w", err)
	}
	if handled {
		return Outcome{
			Decision: Decision{Action: ActionResolveProposal},
			Response: strings.TrimSpace(confirmation),
		}, nil
	}

	decision, err := s.router.Route(ctx, utterance)
	if err != nil {
		return Outcome{}, fmt.Errorf("route utterance: %w", err)
	}

	outcome := Outcome{Decision: decision}
	switch decision.Action {
	case ActionMemoryReview:
		response, reviewErr := s.reviewMemories(ctx, scope, decision)
		outcome.Response = response
		return outcome, reviewErr
	case ActionMemoryForget:
		response, changed, forgetErr := s.forgetMemory(
			ctx,
			scope,
			utteranceID,
			utterance,
			decision,
		)
		outcome.Response = response
		outcome.MemoryChanged = changed
		return outcome, forgetErr
	case ActionMemoryCorrect:
		if strings.TrimSpace(decision.Query) == "" {
			outcome.Response = memoryCorrectionQuestion
		}
		return outcome, nil
	}
	if decision.Action != ActionRespond &&
		decision.Action != ActionStateTransition &&
		decision.Action != ActionProposeTask &&
		decision.Action != ActionProposeWatch {
		return outcome, nil
	}

	query := strings.TrimSpace(decision.Query)
	if query == "" {
		query = strings.TrimSpace(utterance)
	}

	lookup := decision.MemoryLookup
	if strings.TrimSpace(lookup.Query) == "" {
		lookup.Query = query
	}
	lookup.Query = strings.Join(strings.Fields(lookup.Query), " ")

	var (
		cards           []memory.Card
		conversation    session.Conversation
		memoryErr       error
		conversationErr error
		contextGroup    sync.WaitGroup
	)
	contextGroup.Add(2)
	go func() {
		defer contextGroup.Done()
		cards, memoryErr = s.memories.Find(ctx, scope, lookup)
	}()
	go func() {
		defer contextGroup.Done()
		conversation, conversationErr = s.conversation.Prepare(
			ctx,
			scope,
			utteranceID,
			query,
		)
	}()
	contextGroup.Wait()

	if memoryErr != nil {
		slog.WarnContext(ctx, "memory lookup failed", "error", memoryErr)
		cards = nil
	}
	if conversationErr != nil {
		slog.WarnContext(ctx, "conversation context failed", "error", conversationErr)
	}

	// A routing summary is useful for retrieval, but is not the user's request.
	// For ordinary answers preserve explicit source checks, exclusions, budgets
	// and other constraints that a lossy summary may omit. Action-specific
	// rewrites (transitions and proposals) retain their existing behavior.
	agentQuery := query
	if original := strings.TrimSpace(utterance); decision.Action == ActionRespond && original != "" {
		agentQuery = original
	}
	var response string
	if proposalAware, ok := s.agent.(ProposalAwareAgent); ok {
		result, resultErr := proposalAware.RespondWithResult(
			ctx,
			turnScope,
			agentQuery,
			conversation,
			cards,
		)
		response = result.Text
		outcome.ProposalCreated = result.ProposalCreated
		err = resultErr
	} else {
		response, err = s.agent.Respond(ctx, turnScope, agentQuery, conversation, cards)
	}
	if err != nil && outcome.ProposalCreated && ctx.Err() == nil {
		slog.WarnContext(
			ctx,
			"assistant response failed after proposal creation",
			"error", err,
		)
		outcome.Response = proposalResponseFallback
		return outcome, nil
	}
	if err != nil {
		return outcome, fmt.Errorf("generate assistant response: %w", err)
	}
	outcome.Response = strings.TrimSpace(response)

	return outcome, nil
}
