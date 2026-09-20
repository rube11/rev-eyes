package assistant

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func TestMemoryReviewFallbackIsBoundedAndErrorsStayErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failAt    int
		wantCalls int
	}{
		{"no matches", 0, 2}, {"initial failure", 1, 1}, {"fallback failure", 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, agentCalls := 0, 0
			service := NewService(routerFunc(func(context.Context, string) (Decision, error) {
				return Decision{Action: ActionMemoryReview, Query: "school"}, nil
			}),
				agentFunc(func(_ context.Context, scope tool.Scope, query string, _ session.Conversation, cards []memory.Card) (string, error) {
					agentCalls++
					if !scope.MemoryReview || len(cards) != 0 {
						t.Fatal("unexpected review mode")
					}
					return "I didn't find your school in the context I retrieved.", nil
				}), managedMemoryStub{review: func(_ context.Context, _ tool.Scope, lookup memory.Lookup) ([]memory.Card, error) {
					calls++
					if calls == 1 && lookup.Query != "school" {
						t.Fatal("lost specific lookup")
					}
					if calls == 2 && !reflect.DeepEqual(lookup, memory.Lookup{}) {
						t.Fatal("fallback not a profile review")
					}
					if calls == tc.failAt {
						return nil, errors.New("database unavailable")
					}
					return nil, nil
				}}, noConversation, noProposalConfirmation)

			_, err := service.HandleUtterance(context.Background(), tool.Scope{}, "id", "What school do I go to?")
			if calls != tc.wantCalls || (err != nil) != (tc.failAt > 0) {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			if tc.failAt > 0 && agentCalls != 0 {
				t.Fatal("database failure was presented as missing memories")
			}
		})
	}
}
