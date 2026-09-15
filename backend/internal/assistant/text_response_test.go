package assistant

import (
	"context"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func TestTextRespondsWhereAudioStaysSilent(t *testing.T) {
	for _, action := range []Action{ActionIgnore, ActionStateUpdate} {
		for _, alwaysRespond := range []bool{false, true} {
			called := false
			service, err := NewService(
				routerFunc(func(context.Context, string) (Decision, error) {
					return Decision{Action: action, Query: "incidental narration"}, nil
				}),
				agentFunc(func(_ context.Context, scope tool.Scope, query string, _ session.Conversation, _ []memory.Card) (string, error) {
					called = true
					if !scope.AlwaysRespond || query != "hey" {
						t.Fatal("text source or original input lost")
					}
					return "Hey! How can I help?", nil
				}), noMemories, noConversation, noProposalConfirmation)
			if err != nil {
				t.Fatal(err)
			}
			outcome, err := service.HandleUtterance(context.Background(), tool.Scope{AlwaysRespond: alwaysRespond}, "turn", "hey")
			if err != nil {
				t.Fatal(err)
			}
			if called != alwaysRespond {
				t.Fatalf("action %s: agent called=%t for text=%t", action, called, alwaysRespond)
			}
			if alwaysRespond && (outcome.Response == "" || outcome.Decision.Action != ActionRespond) {
				t.Fatal("text was silently ignored")
			}
			if !alwaysRespond && outcome.Response != "" {
				t.Fatal("audio ignore behavior changed")
			}
		}
	}
}

func TestTextKeepsSpecializedRoutes(t *testing.T) {
	for _, action := range []Action{ActionRemember, ActionMemoryCorrect, ActionProposeTask, ActionProposeWatch} {
		service, err := NewService(routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{Action: action, Query: "original request"}, nil
		}),
			proposalAwareAgentFunc(func(context.Context, tool.Scope, string, session.Conversation, []memory.Card) (AgentResult, error) {
				return AgentResult{Text: "Should I save it?", ProposalCreated: true}, nil
			}),
			noMemories, noConversation, noProposalConfirmation)
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.HandleUtterance(context.Background(), tool.Scope{AlwaysRespond: true}, "turn", "original request")
		if err != nil {
			t.Fatal(err)
		}
		if result.Decision.Action != action {
			t.Fatalf("lost specialized route %s", action)
		}
		if (action == ActionProposeTask || action == ActionProposeWatch) && !result.ProposalCreated {
			t.Fatal("lost approval state")
		}
	}
}
