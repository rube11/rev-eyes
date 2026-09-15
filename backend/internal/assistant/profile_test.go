package assistant

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type profileMemoryStub struct {
	managedMemoryStub
	profile func(context.Context, tool.Scope) (string, error)
	edit    func(context.Context, tool.Scope, memory.Lookup, memory.ProfileLayer) (int, error)
}

func (m profileMemoryStub) Profile(ctx context.Context, scope tool.Scope) (string, error) {
	return m.profile(ctx, scope)
}
func (m profileMemoryStub) SetProfileOverride(ctx context.Context, scope tool.Scope, lookup memory.Lookup, layer memory.ProfileLayer) (int, error) {
	return m.edit(ctx, scope, lookup, layer)
}

func TestProfileSuppliedWithoutSearchMatchForChatGlassesAndReview(t *testing.T) {
	for _, action := range []Action{ActionRespond, ActionStateTransition, ActionMemoryReview} {
		for _, typed := range []bool{false, true} {
			for _, failed := range []bool{false, true} {
				loads := 0
				store := profileMemoryStub{managedMemoryStub: managedMemoryStub{review: func(context.Context, tool.Scope, memory.Lookup) ([]memory.Card, error) { return nil, nil }}, profile: func(_ context.Context, scope tool.Scope) (string, error) {
					loads++
					if scope.UserID != "owner" {
						t.Error("profile loaded for wrong owner")
					}
					if failed {
						return "", errors.New("database unavailable")
					}
					return "# User profile\n## Core\n- Student.\n- Protein target: 150g.", nil
				}}
				service, err := NewService(routerFunc(func(context.Context, string) (Decision, error) {
					return Decision{Action: action, Query: "help me decide"}, nil
				}),
					agentFunc(func(_ context.Context, scope tool.Scope, _ string, conversation session.Conversation, cards []memory.Card) (string, error) {
						want := "150g"
						if failed {
							want = "temporarily unavailable"
						}
						if !strings.Contains(conversation.Profile, want) || len(cards) != 0 || scope.AlwaysRespond != typed {
							t.Error("profile missing on shared response path")
						}
						return "Here is a next step.", nil
					}), store, noConversation, noProposalConfirmation)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = service.HandleUtterance(context.Background(), tool.Scope{UserID: "owner", AlwaysRespond: typed}, "turn", "help me decide"); err != nil || loads != 1 {
					t.Fatalf("err=%v loads=%d", err, loads)
				}
			}
		}
	}
}

func TestProfileEditsAreExplicitScopedAndDoNotCallAgent(t *testing.T) {
	for _, action := range []Action{ActionProfileInclude, ActionProfileExclude} {
		for _, ambiguous := range []bool{false, true} {
			calls := 0
			store := profileMemoryStub{edit: func(_ context.Context, scope tool.Scope, lookup memory.Lookup, layer memory.ProfileLayer) (int, error) {
				calls++
				if scope.UserID != "owner" || lookup.Query != "protein target" || (layer == memory.ProfileCore) != (action == ActionProfileInclude) {
					t.Error("incorrect profile edit")
				}
				if ambiguous {
					return 0, memory.ErrMemoryAmbiguous
				}
				return 1, nil
			}}
			service, err := NewService(routerFunc(func(context.Context, string) (Decision, error) {
				return Decision{Action: action, Query: "protein target"}, nil
			}),
				agentFunc(func(context.Context, tool.Scope, string, session.Conversation, []memory.Card) (string, error) {
					t.Error("profile edit called agent")
					return "", nil
				}), store, noConversation, noProposalConfirmation)
			if err != nil {
				t.Fatal(err)
			}
			out, err := service.HandleUtterance(context.Background(), tool.Scope{UserID: "owner"}, "turn", "profile edit")
			if err != nil || calls != 1 || out.MemoryChanged == ambiguous || out.Response == "" {
				t.Fatalf("unexpected result %#v %v", out, err)
			}
		}
	}
}
