package assistant

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const proposalResponseFallback = "I have that ready. Should I save it?"

// ActivityRouter classifies finalized speech using the prepared conversation.
type ActivityRouter interface {
	RouteWithContext(context.Context, string, session.Conversation) (Decision, error)
}

// Agent generates a response for a routed query. Scope must be populated from
// trusted authentication and application-session state by the caller.
type Agent interface {
	RespondWithResult(context.Context, tool.Scope, Action, string, session.Conversation, []memory.Card) (AgentResult, error)
}

// AgentResult includes effects that the response text alone cannot prove.
type AgentResult struct {
	Text            string
	ProposalCreated bool
	ProposalKinds   []ProposalKind
}

type ProposalKind string

const (
	ProposalTask  ProposalKind = "task"
	ProposalWatch ProposalKind = "watch"
)

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
	ProposalKinds   []ProposalKind
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
) *Service {
	if activityRouter == nil || agent == nil || memories == nil || conversation == nil || proposals == nil {
		panic("assistant dependencies are required")
	}

	return &Service{
		router:       activityRouter,
		agent:        agent,
		memories:     memories,
		conversation: conversation,
		proposals:    proposals,
	}
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

	if !scope.Speech.ContextOnly() {
		confirmation, handled, err := s.proposals.Confirm(ctx, turnScope, utterance)
		if err != nil {
			return Outcome{}, fmt.Errorf("confirm proposal: %w", err)
		}
		if handled {
			return Outcome{Decision: Decision{Action: ActionResolveProposal}, Response: strings.TrimSpace(confirmation)}, nil
		}
	}

	var conversation session.Conversation
	var conversationErr error
	prepared := false
	if !shouldIgnore(utterance) {
		// Load once before routing so a clarification can change the lookup.
		// Reuse the same context for the answer; do not add a resolver model call.
		conversation, conversationErr = s.conversation.Prepare(ctx, scope, utteranceID, utterance)
		prepared = true
	}
	conversation.Speech = scope.Speech
	decision, err := s.router.RouteWithContext(ctx, utterance, conversation)
	if err != nil {
		return Outcome{}, fmt.Errorf("route utterance: %w", err)
	}
	// Observed third-party or mixed speech is context, never an account command.
	// Enforce this independently of classifier output and before any mutations.
	if scope.Speech.ContextOnly() && decision.Action != ActionSuggestTip && decision.Action != ActionIgnore && decision.Action != ActionStateUpdate {
		return Outcome{Decision: Decision{Action: ActionIgnore}}, nil
	}

	// App messages are intentional input, not overheard speech. Keep specialized
	// memory/proposal routes, but never silently discard a typed turn.
	if scope.AlwaysRespond && !scope.Speech.ContextOnly() && (decision.Action == ActionIgnore || decision.Action == ActionStateUpdate) {
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
		decision.Action != ActionSuggestTip &&
		decision.Action != ActionStateTransition {
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

	// Retrieval uses the enriched query; the response and tool workflow need the
	// user's actual wording, including self-corrections, tone, and constraints.
	result, err := s.agent.RespondWithResult(ctx, turnScope, decision.Action, strings.TrimSpace(utterance), conversation, cards)
	outcome.ProposalCreated = result.ProposalCreated
	outcome.ProposalKinds = result.ProposalKinds
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
	outcome.Response = strings.TrimSpace(result.Text)

	return outcome, nil
}
