package assistant

import (
	"context"
	"errors"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func TestHandleUtteranceRoutesTaskCandidateToAgent(t *testing.T) {
	t.Parallel()

	agentCalled := false
	service, err := NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{
				Action: ActionProposeTask,
				Query:  "I should call my dentist tomorrow.",
			}, nil
		}),
		agentFunc(func(
			context.Context,
			tool.Scope,
			string,
			session.Conversation,
			[]memory.Card,
		) (string, error) {
			agentCalled = true
			return "Want me to save that reminder?", nil
		}),
		noMemories,
		noConversation,
		noProposalConfirmation,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	outcome, err := service.HandleUtterance(
		context.Background(),
		tool.Scope{},
		"utterance-1",
		"I should call my dentist tomorrow.",
	)
	if err != nil {
		t.Fatalf("HandleUtterance() error = %v", err)
	}
	if !agentCalled {
		t.Fatal("Respond() was not called")
	}
	if outcome.Decision.Action != ActionProposeTask ||
		outcome.Response != "Want me to save that reminder?" {
		t.Fatalf("outcome = %#v", outcome)
	}
}

func TestHandleUtteranceResolvesTaskBeforeRouting(t *testing.T) {
	t.Parallel()

	wantScope := tool.Scope{UserID: "user-1", SessionID: "session-1"}
	wantTurnScope := wantScope
	wantTurnScope.UtteranceID = "utterance-2"
	service, err := NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			t.Fatal("Route() was called")
			return Decision{}, nil
		}),
		agentFunc(func(
			context.Context,
			tool.Scope,
			string,
			session.Conversation,
			[]memory.Card,
		) (string, error) {
			t.Fatal("Respond() was called")
			return "", nil
		}),
		noMemories,
		noConversation,
		proposalConfirmerFunc(func(
			_ context.Context,
			scope tool.Scope,
			utterance string,
		) (string, bool, error) {
			if scope != wantTurnScope || utterance != "yes please" {
				t.Fatalf("Confirm() scope = %#v, utterance = %q", scope, utterance)
			}
			return "  Okay, I saved that reminder.  ", true, nil
		}),
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	outcome, err := service.HandleUtterance(
		context.Background(),
		wantScope,
		"utterance-2",
		"yes please",
	)
	if err != nil {
		t.Fatalf("HandleUtterance() error = %v", err)
	}
	if outcome.Decision.Action != ActionResolveProposal ||
		outcome.Response != "Okay, I saved that reminder." {
		t.Fatalf("outcome = %#v", outcome)
	}
}

func TestHandleUtteranceReportsTaskConfirmationFailure(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("database unavailable")
	service, err := NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			t.Fatal("Route() was called")
			return Decision{}, nil
		}),
		agentFunc(func(context.Context, tool.Scope, string, session.Conversation, []memory.Card) (string, error) {
			return "", nil
		}),
		noMemories,
		noConversation,
		proposalConfirmerFunc(func(context.Context, tool.Scope, string) (string, bool, error) {
			return "", false, wantErr
		}),
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	_, err = service.HandleUtterance(context.Background(), tool.Scope{}, "utterance-1", "yes")
	if !errors.Is(err, wantErr) {
		t.Fatalf("HandleUtterance() error = %v", err)
	}
}
