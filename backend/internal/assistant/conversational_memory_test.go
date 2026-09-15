package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func TestContextualRouterBoundsHistoryAndPreservesAudioIgnore(t *testing.T) {
	history := session.Conversation{}
	for range 10 {
		history.Messages = append(history.Messages, session.Message{Speaker: session.SpeakerUser, Text: strings.Repeat("é", 1200)})
	}
	calls := 0
	router := NewRouter(func(_ context.Context, input string) (string, error) {
		calls++
		var envelope struct {
			Recent []struct {
				Speaker string `json:"speaker"`
				Text    string `json:"text"`
			} `json:"recent_dialogue"`
			Latest string `json:"latest_utterance"`
		}
		if err := json.Unmarshal([]byte(input), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Latest != "I meant me" || len(envelope.Recent) != 6 {
			t.Fatalf("unexpected context: %#v", envelope)
		}
		for _, turn := range envelope.Recent {
			if len([]rune(turn.Text)) != 800 || turn.Speaker != "user" {
				t.Fatal("unbounded or mislabelled history")
			}
		}
		return `{"action":"memory_review","query":"irrelevant generic query","memory_review_all":true,"memory_lookup":{"terms":["me"]}}`, nil
	})
	decision, err := router.RouteWithContext(context.Background(), "I meant me", history)
	if err != nil || !decision.MemoryReviewAll || !decision.MemoryLookup.Empty() || len(decision.MemoryLookup.Topics) != 0 || len(decision.MemoryLookup.Kinds) != 0 || decision.Query != "" {
		t.Fatalf("profile decision: %#v %v", decision, err)
	}
	decision, err = router.RouteWithContext(context.Background(), "um", history)
	if err != nil || decision.Action != ActionIgnore || calls != 1 {
		t.Fatalf("audio ignore changed: %#v %v calls=%d", decision, err, calls)
	}
}

func TestMemoryReviewUsesContextAndExplicitProfileMode(t *testing.T) {
	ctx := context.Background()
	prior := session.Conversation{Messages: []session.Message{
		{Speaker: session.SpeakerUser, Text: "waht do you konw about e"},
		{Speaker: session.SpeakerAssistant, Text: "What does e mean?"},
	}}
	loads, reviews, classifications := 0, 0, 0
	cards := []memory.Card{{Title: "University", Summary: "The user attends UNLV."}}
	router := NewRouter(func(_ context.Context, input string) (string, error) {
		classifications++
		if !strings.Contains(input, "waht do you konw about e") || !strings.Contains(input, "I meant me") {
			t.Fatal("router missing repair context")
		}
		return `{"action":"memory_review","query":"what do you know about me","memory_review_all":true}`, nil
	})
	service, err := NewService(router, agentFunc(func(_ context.Context, scope tool.Scope, query string, got session.Conversation, memories []memory.Card) (string, error) {
		if query != "I meant me" || !scope.MemoryReview || scope.UtteranceID != "current" || !reflect.DeepEqual(got, prior) || !reflect.DeepEqual(memories, cards) {
			t.Fatalf("agent lost original context: %q %#v", query, scope)
		}
		return "You're at UNLV. I misread your earlier question.", nil
	}), managedMemoryStub{review: func(_ context.Context, _ tool.Scope, lookup memory.Lookup) ([]memory.Card, error) {
		reviews++
		if !reflect.DeepEqual(lookup, memory.Lookup{}) {
			t.Fatalf("profile review became keyword search: %#v", lookup)
		}
		return cards, nil
	}}, conversationReaderFunc(func(_ context.Context, _ tool.Scope, id, text string) (session.Conversation, error) {
		loads++
		if id != "current" || text != "I meant me" {
			t.Fatal("context loaded for wrong turn")
		}
		return prior, nil
	}), noProposalConfirmation)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.HandleUtterance(ctx, tool.Scope{UserID: "owner", SessionID: "chat"}, "current", "I meant me")
	if err != nil || result.Response == "" || result.MemoryChanged || result.ProposalCreated || loads != 1 || reviews != 1 || classifications != 1 {
		t.Fatalf("result=%#v err=%v loads=%d reviews=%d classifications=%d", result, err, loads, reviews, classifications)
	}
}

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
			service, err := NewService(routerFunc(func(context.Context, string) (Decision, error) {
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
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.HandleUtterance(context.Background(), tool.Scope{}, "id", "What school do I go to?")
			if calls != tc.wantCalls || (err != nil) != (tc.failAt > 0) {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			if tc.failAt > 0 && agentCalls != 0 {
				t.Fatal("database failure was presented as missing memories")
			}
		})
	}
}

func TestContextualResponseStillRetrievesMemoriesAndLoadsConversationOnce(t *testing.T) {
	loads, finds := 0, 0
	service, err := NewService(NewRouter(func(context.Context, string) (string, error) {
		return `{"action":"respond","query":"Dinner after the gym","memory_lookup":{"terms":["food preference"]}}`, nil
	}), agentFunc(func(_ context.Context, _ tool.Scope, _ string, _ session.Conversation, cards []memory.Card) (string, error) {
		if len(cards) != 1 {
			t.Fatal("contextual routing skipped memory retrieval")
		}
		return "Chicken and rice would fit.", nil
	}), memoryReaderFunc(func(context.Context, tool.Scope, memory.Lookup) ([]memory.Card, error) {
		finds++
		return []memory.Card{{Summary: "Likes chicken and rice."}}, nil
	}), conversationReaderFunc(func(context.Context, tool.Scope, string, string) (session.Conversation, error) {
		loads++
		return session.Conversation{}, nil
	}), noProposalConfirmation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.HandleUtterance(context.Background(), tool.Scope{}, "id", "what should I eat"); err != nil {
		t.Fatal(err)
	}
	if loads != 1 || finds != 1 {
		t.Fatalf("loads=%d finds=%d", loads, finds)
	}
}
