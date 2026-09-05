package assistant

import (
	"context"
	"reflect"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func TestHandleUtteranceReviewsMemoryWithoutGeneralAgent(t *testing.T) {
	wantLookup := memory.Lookup{Query: "Jolene", Entities: []string{"jolene"}}
	service, err := NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{
				Action:       ActionMemoryReview,
				Query:        "Jolene",
				MemoryLookup: wantLookup,
			}, nil
		}),
		agentFunc(func(
			context.Context,
			tool.Scope,
			string,
			session.Conversation,
			[]memory.Card,
		) (string, error) {
			t.Fatal("general agent was called for deterministic memory review")
			return "", nil
		}),
		managedMemoryStub{review: func(
			_ context.Context,
			_ tool.Scope,
			lookup memory.Lookup,
		) ([]memory.Card, error) {
			if !reflect.DeepEqual(lookup, wantLookup) {
				t.Fatalf("Review() lookup = %#v", lookup)
			}
			return []memory.Card{{
				Topics:  []memory.Topic{memory.TopicFriends},
				Kind:    memory.KindPreference,
				Title:   "Jolene's favorite food",
				Summary: "Jolene likes pho.",
			}}, nil
		}},
		noConversation,
		noProposalConfirmation,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	outcome, err := service.HandleUtterance(
		context.Background(),
		tool.Scope{UserID: "user-1", SessionID: "session-1"},
		"utterance-1",
		"What do you remember about Jolene?",
	)
	if err != nil {
		t.Fatalf("HandleUtterance() error = %v", err)
	}
	want := "I remember: Jolene's favorite food — Jolene likes pho."
	if outcome.Response != want || outcome.MemoryChanged {
		t.Fatalf("outcome = %#v, want response %q", outcome, want)
	}
}

func TestHandleUtteranceForgetsSingularReviewedMemoryByContext(t *testing.T) {
	service, err := NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{Action: ActionMemoryForget}, nil
		}),
		agentFunc(func(
			context.Context,
			tool.Scope,
			string,
			session.Conversation,
			[]memory.Card,
		) (string, error) {
			t.Fatal("general agent was called for memory removal")
			return "", nil
		}),
		managedMemoryStub{
			review: func(context.Context, tool.Scope, memory.Lookup) ([]memory.Card, error) {
				return nil, nil
			},
			forget: func(
				_ context.Context,
				_ tool.Scope,
				lookup memory.Lookup,
			) (int, error) {
				want := memory.Lookup{Terms: []string{"Jolene's favorite food"}}
				if !reflect.DeepEqual(lookup, want) {
					t.Fatalf("Forget() lookup = %#v, want %#v", lookup, want)
				}
				return 1, nil
			},
		},
		conversationReaderFunc(func(
			context.Context,
			tool.Scope,
			string,
			string,
		) (session.Conversation, error) {
			return session.Conversation{Messages: []session.Message{{
				Speaker: session.SpeakerAssistant,
				Text:    "I remember: Jolene's favorite food — Jolene likes pho.",
			}}}, nil
		}),
		noProposalConfirmation,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	outcome, err := service.HandleUtterance(
		context.Background(),
		tool.Scope{UserID: "user-1", SessionID: "session-1"},
		"utterance-2",
		"Forget that.",
	)
	if err != nil {
		t.Fatalf("HandleUtterance() error = %v", err)
	}
	if outcome.Response != "Okay, I forgot that." || !outcome.MemoryChanged {
		t.Fatalf("outcome = %#v", outcome)
	}
}

func TestHandleUtteranceDoesNotForgetStaleReviewedMemory(t *testing.T) {
	service, err := NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{Action: ActionMemoryForget}, nil
		}),
		agentFunc(func(
			context.Context,
			tool.Scope,
			string,
			session.Conversation,
			[]memory.Card,
		) (string, error) {
			return "", nil
		}),
		managedMemoryStub{
			review: func(context.Context, tool.Scope, memory.Lookup) ([]memory.Card, error) {
				return nil, nil
			},
			forget: func(context.Context, tool.Scope, memory.Lookup) (int, error) {
				t.Fatal("Forget() was called for a stale contextual reference")
				return 0, nil
			},
		},
		conversationReaderFunc(func(
			context.Context,
			tool.Scope,
			string,
			string,
		) (session.Conversation, error) {
			return session.Conversation{Messages: []session.Message{
				{Speaker: session.SpeakerAssistant, Text: "I remember: Jolene's favorite food — Jolene likes pho."},
				{Speaker: session.SpeakerUser, Text: "What is on my schedule?"},
				{Speaker: session.SpeakerAssistant, Text: "You have class at three."},
			}}, nil
		}),
		noProposalConfirmation,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	outcome, err := service.HandleUtterance(
		context.Background(),
		tool.Scope{UserID: "user-1", SessionID: "session-1"},
		"utterance-4",
		"Forget that.",
	)
	if err != nil {
		t.Fatalf("HandleUtterance() error = %v", err)
	}
	if outcome.Response != memoryForgetQuestion || outcome.MemoryChanged {
		t.Fatalf("outcome = %#v", outcome)
	}
}

func TestHandleUtterancePreservesSpecificForgetQueryWithEntity(t *testing.T) {
	wantLookup := memory.Lookup{
		Query:    "Jolene likes pho",
		Entities: []string{"jolene"},
	}
	service, err := NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{
				Action:       ActionMemoryForget,
				Query:        "Jolene likes pho",
				MemoryLookup: memory.Lookup{Entities: []string{"jolene"}},
			}, nil
		}),
		agentFunc(func(
			context.Context,
			tool.Scope,
			string,
			session.Conversation,
			[]memory.Card,
		) (string, error) {
			return "", nil
		}),
		managedMemoryStub{
			review: func(context.Context, tool.Scope, memory.Lookup) ([]memory.Card, error) {
				return nil, nil
			},
			forget: func(
				_ context.Context,
				_ tool.Scope,
				lookup memory.Lookup,
			) (int, error) {
				if !reflect.DeepEqual(lookup, wantLookup) {
					t.Fatalf("Forget() lookup = %#v, want %#v", lookup, wantLookup)
				}
				return 1, nil
			},
		},
		noConversation,
		noProposalConfirmation,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	outcome, err := service.HandleUtterance(
		context.Background(),
		tool.Scope{UserID: "user-1", SessionID: "session-1"},
		"utterance-1",
		"Forget that Jolene likes pho.",
	)
	if err != nil {
		t.Fatalf("HandleUtterance() error = %v", err)
	}
	if outcome.Response != "Okay, I forgot that." || !outcome.MemoryChanged {
		t.Fatalf("outcome = %#v", outcome)
	}
}

func TestHandleUtteranceAsksForMissingCorrection(t *testing.T) {
	service, err := NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{Action: ActionMemoryCorrect}, nil
		}),
		agentFunc(func(
			context.Context,
			tool.Scope,
			string,
			session.Conversation,
			[]memory.Card,
		) (string, error) {
			t.Fatal("general agent was called for incomplete memory correction")
			return "", nil
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
		"That's wrong.",
	)
	if err != nil {
		t.Fatalf("HandleUtterance() error = %v", err)
	}
	if outcome.Response != memoryCorrectionQuestion || outcome.MemoryChanged {
		t.Fatalf("outcome = %#v", outcome)
	}
}
