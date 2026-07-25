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

type routerFunc func(ctx context.Context, utterance string) (Decision, error)

func (f routerFunc) Route(ctx context.Context, utterance string) (Decision, error) {
	return f(ctx, utterance)
}

type agentFunc func(
	context.Context,
	tool.Scope,
	string,
	session.Conversation,
	[]memory.Card,
) (string, error)

func (f agentFunc) Respond(
	ctx context.Context,
	scope tool.Scope,
	query string,
	conversation session.Conversation,
	memories []memory.Card,
) (string, error) {
	return f(ctx, scope, query, conversation, memories)
}

type memoryReaderFunc func(context.Context, tool.Scope, memory.Lookup) ([]memory.Card, error)

func (f memoryReaderFunc) Find(
	ctx context.Context,
	scope tool.Scope,
	lookup memory.Lookup,
) ([]memory.Card, error) {
	return f(ctx, scope, lookup)
}

var noMemories = memoryReaderFunc(func(
	context.Context,
	tool.Scope,
	memory.Lookup,
) ([]memory.Card, error) {
	return nil, nil
})

type conversationReaderFunc func(
	context.Context,
	tool.Scope,
	string,
	string,
) (session.Conversation, error)

func (f conversationReaderFunc) Prepare(
	ctx context.Context,
	scope tool.Scope,
	utteranceID string,
	text string,
) (session.Conversation, error) {
	return f(ctx, scope, utteranceID, text)
}

var noConversation = conversationReaderFunc(func(
	context.Context,
	tool.Scope,
	string,
	string,
) (session.Conversation, error) {
	return session.Conversation{}, nil
})

type proposalConfirmerFunc func(context.Context, tool.Scope, string) (string, bool, error)

func (f proposalConfirmerFunc) Confirm(
	ctx context.Context,
	scope tool.Scope,
	utterance string,
) (string, bool, error) {
	return f(ctx, scope, utterance)
}

var noProposalConfirmation = proposalConfirmerFunc(func(
	context.Context,
	tool.Scope,
	string,
) (string, bool, error) {
	return "", false, nil
})

func TestHandleUtteranceRespondsWithRoutedQueryAndTrustedScope(t *testing.T) {
	t.Parallel()

	wantScope := tool.Scope{UserID: "user-123", SessionID: "session-456"}
	wantAgentScope := wantScope
	wantAgentScope.UtteranceID = "utterance-789"
	wantLookup := memory.Lookup{
		Terms:  []string{"cafe"},
		Topics: []memory.Topic{memory.TopicPlaces},
	}
	wantMemories := []memory.Card{{
		Topics:  []memory.Topic{memory.TopicPlaces},
		Kind:    memory.KindPreference,
		Title:   "Favorite cafe",
		Summary: "The user likes Harbor Cafe.",
	}}
	wantConversation := session.Conversation{
		Summary: "The user is looking for somewhere to eat.",
		Messages: []session.Message{{
			ID:      "prior-1",
			Speaker: session.SpeakerAssistant,
			Text:    "What kind of food?",
		}},
	}
	agentCalled := false
	service, err := NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{
				Action:       ActionRespond,
				Query:        "What is nearby?",
				MemoryLookup: wantLookup,
			}, nil
		}),
		agentFunc(func(
			_ context.Context,
			scope tool.Scope,
			query string,
			conversation session.Conversation,
			memories []memory.Card,
		) (string, error) {
			agentCalled = true
			if scope != wantAgentScope {
				t.Fatalf("Respond() scope = %#v, want %#v", scope, wantAgentScope)
			}
			if query != "What is nearby?" {
				t.Fatalf("Respond() query = %q", query)
			}
			if !reflect.DeepEqual(conversation, wantConversation) {
				t.Fatalf("Respond() conversation = %#v, want %#v", conversation, wantConversation)
			}
			if !reflect.DeepEqual(memories, wantMemories) {
				t.Fatalf("Respond() memories = %#v, want %#v", memories, wantMemories)
			}
			return "  There is a cafe nearby.  ", nil
		}),
		memoryReaderFunc(func(
			_ context.Context,
			scope tool.Scope,
			lookup memory.Lookup,
		) ([]memory.Card, error) {
			if scope != wantScope || !reflect.DeepEqual(lookup, wantLookup) {
				t.Fatalf("Find() scope = %#v, lookup = %#v", scope, lookup)
			}
			return wantMemories, nil
		}),
		conversationReaderFunc(func(
			_ context.Context,
			scope tool.Scope,
			utteranceID string,
			text string,
		) (session.Conversation, error) {
			if scope != wantScope || utteranceID != "utterance-789" || text != "What is nearby?" {
				t.Fatalf("Prepare() scope = %#v, utterance ID = %q, text = %q", scope, utteranceID, text)
			}
			return wantConversation, nil
		}),
		noProposalConfirmation,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	outcome, err := service.HandleUtterance(
		context.Background(),
		wantScope,
		"utterance-789",
		"what's around here",
	)
	if err != nil {
		t.Fatalf("HandleUtterance() error = %v", err)
	}
	if !agentCalled {
		t.Fatal("Respond() was not called")
	}
	if outcome.Decision.Action != ActionRespond {
		t.Fatalf("outcome action = %q", outcome.Decision.Action)
	}
	if outcome.Response != "There is a cafe nearby." {
		t.Fatalf("outcome response = %q", outcome.Response)
	}
}

func TestHandleUtteranceDoesNotCallAgentForNonResponseAction(t *testing.T) {
	t.Parallel()

	service, err := NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{Action: ActionStateUpdate}, nil
		}),
		agentFunc(func(context.Context, tool.Scope, string, session.Conversation, []memory.Card) (string, error) {
			t.Fatal("Respond() called for state update")
			return "", nil
		}),
		memoryReaderFunc(func(context.Context, tool.Scope, memory.Lookup) ([]memory.Card, error) {
			t.Fatal("Find() called for state update")
			return nil, nil
		}),
		conversationReaderFunc(func(context.Context, tool.Scope, string, string) (session.Conversation, error) {
			t.Fatal("Prepare() called for state update")
			return session.Conversation{}, nil
		}),
		noProposalConfirmation,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	outcome, err := service.HandleUtterance(context.Background(), tool.Scope{}, "utterance-1", "I'm at work")
	if err != nil {
		t.Fatalf("HandleUtterance() error = %v", err)
	}
	if outcome.Decision.Action != ActionStateUpdate {
		t.Fatalf("outcome action = %q", outcome.Decision.Action)
	}
	if outcome.Response != "" {
		t.Fatalf("outcome response = %q, want empty", outcome.Response)
	}
}

func TestHandleUtteranceUsesOriginalUtteranceWhenQueryIsEmpty(t *testing.T) {
	t.Parallel()

	service, err := NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{Action: ActionRespond}, nil
		}),
		agentFunc(func(_ context.Context, _ tool.Scope, query string, _ session.Conversation, _ []memory.Card) (string, error) {
			if query != "what time is it" {
				t.Fatalf("Respond() query = %q", query)
			}
			return "It is noon.", nil
		}),
		noMemories,
		noConversation,
		noProposalConfirmation,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	if _, err := service.HandleUtterance(
		context.Background(),
		tool.Scope{},
		"utterance-1",
		"  what time is it  ",
	); err != nil {
		t.Fatalf("HandleUtterance() error = %v", err)
	}
}

func TestHandleUtteranceWrapsDependencyErrors(t *testing.T) {
	t.Parallel()

	routeErr := errors.New("route failed")
	service, err := NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{}, routeErr
		}),
		agentFunc(func(context.Context, tool.Scope, string, session.Conversation, []memory.Card) (string, error) {
			return "", nil
		}),
		noMemories,
		noConversation,
		noProposalConfirmation,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := service.HandleUtterance(context.Background(), tool.Scope{}, "utterance-1", "hello"); !errors.Is(err, routeErr) {
		t.Fatalf("HandleUtterance() error = %v, want wrapped route error", err)
	}

	agentErr := errors.New("agent failed")
	service, err = NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{Action: ActionRespond, Query: "hello"}, nil
		}),
		agentFunc(func(context.Context, tool.Scope, string, session.Conversation, []memory.Card) (string, error) {
			return "", agentErr
		}),
		noMemories,
		noConversation,
		noProposalConfirmation,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := service.HandleUtterance(context.Background(), tool.Scope{}, "utterance-1", "hello"); !errors.Is(err, agentErr) {
		t.Fatalf("HandleUtterance() error = %v, want wrapped agent error", err)
	}
}

func TestHandleUtteranceContinuesWhenMemoryLookupFails(t *testing.T) {
	t.Parallel()

	service, err := NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{Action: ActionRespond, Query: "hello"}, nil
		}),
		agentFunc(func(
			context.Context,
			tool.Scope,
			string,
			session.Conversation,
			[]memory.Card,
		) (string, error) {
			return "Hello.", nil
		}),
		memoryReaderFunc(func(context.Context, tool.Scope, memory.Lookup) ([]memory.Card, error) {
			return nil, errors.New("database unavailable")
		}),
		noConversation,
		noProposalConfirmation,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	outcome, err := service.HandleUtterance(context.Background(), tool.Scope{}, "utterance-1", "hello")
	if err != nil {
		t.Fatalf("HandleUtterance() error = %v", err)
	}
	if outcome.Response != "Hello." {
		t.Fatalf("response = %q", outcome.Response)
	}
}

func TestHandleUtteranceContinuesWhenConversationPreparationFails(t *testing.T) {
	t.Parallel()

	wantConversation := session.Conversation{Summary: "Recovered context."}
	service, err := NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{Action: ActionRespond, Query: "hello"}, nil
		}),
		agentFunc(func(
			_ context.Context,
			_ tool.Scope,
			_ string,
			conversation session.Conversation,
			_ []memory.Card,
		) (string, error) {
			if !reflect.DeepEqual(conversation, wantConversation) {
				t.Fatalf("Respond() conversation = %#v, want %#v", conversation, wantConversation)
			}
			return "Hello.", nil
		}),
		noMemories,
		conversationReaderFunc(func(
			context.Context,
			tool.Scope,
			string,
			string,
		) (session.Conversation, error) {
			return wantConversation, errors.New("save failed")
		}),
		noProposalConfirmation,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	outcome, err := service.HandleUtterance(
		context.Background(),
		tool.Scope{},
		"utterance-1",
		"hello",
	)
	if err != nil {
		t.Fatalf("HandleUtterance() error = %v", err)
	}
	if outcome.Response != "Hello." {
		t.Fatalf("response = %q", outcome.Response)
	}
}

func TestNewServiceRequiresDependencies(t *testing.T) {
	t.Parallel()

	agent := agentFunc(func(context.Context, tool.Scope, string, session.Conversation, []memory.Card) (string, error) {
		return "", nil
	})
	activityRouter := routerFunc(func(context.Context, string) (Decision, error) {
		return Decision{}, nil
	})

	if _, err := NewService(nil, agent, noMemories, noConversation, noProposalConfirmation); !errors.Is(err, ErrRouterRequired) {
		t.Fatalf("NewService(nil, agent) error = %v", err)
	}
	if _, err := NewService(activityRouter, nil, noMemories, noConversation, noProposalConfirmation); !errors.Is(err, ErrAgentRequired) {
		t.Fatalf("NewService(router, nil) error = %v", err)
	}
	if _, err := NewService(activityRouter, agent, nil, noConversation, noProposalConfirmation); !errors.Is(err, ErrMemoryRequired) {
		t.Fatalf("NewService(router, agent, nil) error = %v", err)
	}
	if _, err := NewService(activityRouter, agent, noMemories, nil, noProposalConfirmation); !errors.Is(err, ErrConversationRequired) {
		t.Fatalf("NewService(router, agent, memories, nil) error = %v", err)
	}
	if _, err := NewService(activityRouter, agent, noMemories, noConversation, nil); !errors.Is(err, ErrProposalConfirmerRequired) {
		t.Fatalf("NewService(router, agent, memories, conversation, nil) error = %v", err)
	}
}
