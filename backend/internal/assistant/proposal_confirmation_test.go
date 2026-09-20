package assistant

import (
	"context"
	"errors"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func TestHandleUtteranceResolvesTaskBeforeRouting(t *testing.T) {
	t.Parallel()

	wantScope := tool.Scope{UserID: "user-1", SessionID: "session-1"}
	wantTurnScope := wantScope
	wantTurnScope.UtteranceID = "utterance-2"
	service := NewService(
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
	service := NewService(
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

	_, err := service.HandleUtterance(context.Background(), tool.Scope{}, "utterance-1", "yes")
	if !errors.Is(err, wantErr) {
		t.Fatalf("HandleUtterance() error = %v", err)
	}
}
