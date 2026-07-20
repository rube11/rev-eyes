package assistant

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

var (
	ErrRouterRequired = errors.New("assistant router is required")
	ErrAgentRequired  = errors.New("assistant agent is required")
)

// ActivityRouter decides how the assistant should handle a finalized utterance.
type ActivityRouter interface {
	Route(ctx context.Context, utterance string) (Decision, error)
}

// Agent generates a response for a routed query. Scope must be populated from
// trusted authentication and application-session state by the caller.
type Agent interface {
	Respond(ctx context.Context, scope tool.Scope, query string) (string, error)
}

// Outcome describes what the assistant decided and any response it generated.
type Outcome struct {
	Decision Decision
	Response string
}

// Service coordinates routing and response generation.
type Service struct {
	router ActivityRouter
	agent  Agent
}

func NewService(activityRouter ActivityRouter, agent Agent) (*Service, error) {
	if activityRouter == nil {
		return nil, ErrRouterRequired
	}
	if agent == nil {
		return nil, ErrAgentRequired
	}

	return &Service{
		router: activityRouter,
		agent:  agent,
	}, nil
}

// HandleUtterance routes finalized speech and calls the agent only when the
// router selects the respond action.
func (s *Service) HandleUtterance(
	ctx context.Context,
	scope tool.Scope,
	utterance string,
) (Outcome, error) {
	decision, err := s.router.Route(ctx, utterance)
	if err != nil {
		return Outcome{}, fmt.Errorf("route utterance: %w", err)
	}

	outcome := Outcome{Decision: decision}
	if decision.Action != ActionRespond {
		return outcome, nil
	}

	query := strings.TrimSpace(decision.Query)
	if query == "" {
		query = strings.TrimSpace(utterance)
	}

	response, err := s.agent.Respond(ctx, scope, query)
	if err != nil {
		return outcome, fmt.Errorf("generate assistant response: %w", err)
	}
	outcome.Response = strings.TrimSpace(response)

	return outcome, nil
}
