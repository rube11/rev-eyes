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

type contextualRouter interface {
	RouteWithContext(context.Context, string, session.Conversation) (Decision, error)
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
	Profile(ctx context.Context, scope tool.Scope) (string, error)
	SetProfileOverride(ctx context.Context, scope tool.Scope, lookup memory.Lookup, layer memory.ProfileLayer) (int, error)
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

	var conversation session.Conversation
	var conversationErr error
	var decision Decision
	prepared := false
	if router, ok := s.router.(contextualRouter); ok && !shouldIgnore(utterance) {
		// Load once before routing so a clarification can change the lookup.
		// Reuse the same context for the answer; do not add a resolver model call.
		conversation, conversationErr = s.conversation.Prepare(ctx, scope, utteranceID, utterance)
		prepared = true
		decision, err = router.RouteWithContext(ctx, utterance, conversation)
	} else {
		decision, err = s.router.Route(ctx, utterance)
	}
	if err != nil {
		return Outcome{}, fmt.Errorf("route utterance: %w", err)
	}

	// App messages are intentional input, not overheard speech. Keep specialized
	// memory/proposal routes, but never silently discard a typed turn.
	if scope.AlwaysRespond && (decision.Action == ActionIgnore || decision.Action == ActionStateUpdate) {
		decision.Action = ActionRespond
		decision.Query = strings.TrimSpace(utterance)
	}
	outcome := Outcome{Decision: decision}
	switch decision.Action {
	case ActionProfileInclude, ActionProfileExclude:
		response, changed, profileErr := s.changeProfile(ctx, turnScope, decision)
		outcome.Response = response
		outcome.MemoryChanged = changed
		return outcome, profileErr
	case ActionMemoryReview:
		if !prepared {
			conversation, conversationErr = s.conversation.Prepare(ctx, scope, utteranceID, utterance)
		}
		if conversationErr != nil {
			slog.WarnContext(ctx, "memory review context failed", "error", conversationErr)
		}
		response, reviewErr := s.reviewMemories(ctx, turnScope, decision, utterance, conversation)
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
		cards        []memory.Card
		memoryErr    error
		profile      string
		contextGroup sync.WaitGroup
	)
	contextGroup.Add(3)
	go func() {
		defer contextGroup.Done()
		profile = s.loadProfile(ctx, scope)
	}()
	go func() {
		defer contextGroup.Done()
		cards, memoryErr = s.memories.Find(ctx, scope, lookup)
	}()
	go func() {
		defer contextGroup.Done()
		if prepared {
			return
		}
		conversation, conversationErr = s.conversation.Prepare(
			ctx,
			scope,
			utteranceID,
			query,
		)
	}()
	contextGroup.Wait()
	conversation.Profile = profile

	if memoryErr != nil {
		slog.WarnContext(ctx, "memory lookup failed", "error", memoryErr)
		cards = nil
	}
	if conversationErr != nil {
		slog.WarnContext(ctx, "conversation context failed", "error", conversationErr)
	}

	var response string
	if proposalAware, ok := s.agent.(ProposalAwareAgent); ok {
		result, resultErr := proposalAware.RespondWithResult(
			ctx,
			turnScope,
			query,
			conversation,
			cards,
		)
		response = result.Text
		outcome.ProposalCreated = result.ProposalCreated
		err = resultErr
	} else {
		response, err = s.agent.Respond(ctx, turnScope, query, conversation, cards)
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
