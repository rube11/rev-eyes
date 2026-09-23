package assistant

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type routerFunc func(context.Context, string) (Decision, error)

func (f routerFunc) RouteWithContext(ctx context.Context, utterance string, _ session.Conversation) (Decision, error) {
	return f(ctx, utterance)
}

type agentFunc func(
	context.Context,
	tool.Scope,
	string,
	session.Conversation,
	[]memory.Card,
) (string, error)

func (f agentFunc) RespondWithResult(ctx context.Context, scope tool.Scope, _ Action, query string, conversation session.Conversation, memories []memory.Card) (AgentResult, error) {
	text, err := f(ctx, scope, query, conversation, memories)
	return AgentResult{Text: text}, err
}

type proposalAwareAgentFunc func(
	context.Context,
	tool.Scope,
	string,
	session.Conversation,
	[]memory.Card,
) (AgentResult, error)

func (f proposalAwareAgentFunc) RespondWithResult(
	ctx context.Context,
	scope tool.Scope,
	_ Action,
	query string,
	conversation session.Conversation,
	memories []memory.Card,
) (AgentResult, error) {
	return f(ctx, scope, query, conversation, memories)
}

type routeRecordingAgent struct {
	action   Action
	query    string
	profile  string
	memories []memory.Card
}

func (a *routeRecordingAgent) RespondWithResult(
	_ context.Context,
	_ tool.Scope,
	action Action,
	query string,
	conversation session.Conversation,
	memories []memory.Card,
) (AgentResult, error) {
	a.action = action
	a.query = query
	a.profile = conversation.Profile
	a.memories = memories
	return AgentResult{Text: "Try one distraction-free twenty-minute block."}, nil
}

type memoryReaderFunc func(context.Context, tool.Scope, memory.Lookup) ([]memory.Card, error)

func (f memoryReaderFunc) Find(
	ctx context.Context,
	scope tool.Scope,
	lookup memory.Lookup,
) ([]memory.Card, error) {
	return f(ctx, scope, lookup)
}

func (f memoryReaderFunc) Review(
	ctx context.Context,
	scope tool.Scope,
	lookup memory.Lookup,
) ([]memory.Card, error) {
	return f(ctx, scope, lookup)
}

func (f memoryReaderFunc) Forget(
	context.Context,
	tool.Scope,
	memory.Lookup,
) (int, error) {
	return 0, nil
}

type managedMemoryStub struct {
	find   func(context.Context, tool.Scope, memory.Lookup) ([]memory.Card, error)
	review func(context.Context, tool.Scope, memory.Lookup) ([]memory.Card, error)
	forget func(context.Context, tool.Scope, memory.Lookup) (int, error)
}

func (f memoryReaderFunc) Profile(context.Context, tool.Scope) (string, error) {
	return "", nil
}

func (f memoryReaderFunc) SetProfileOverride(context.Context, tool.Scope, memory.Lookup, memory.ProfileLayer) (int, error) {
	return 0, nil
}

func (m managedMemoryStub) Profile(context.Context, tool.Scope) (string, error) {
	return "", nil
}

func (m managedMemoryStub) SetProfileOverride(context.Context, tool.Scope, memory.Lookup, memory.ProfileLayer) (int, error) {
	return 0, nil
}

func (m managedMemoryStub) Find(
	ctx context.Context,
	scope tool.Scope,
	lookup memory.Lookup,
) ([]memory.Card, error) {
	if m.find == nil {
		return nil, nil
	}
	return m.find(ctx, scope, lookup)
}

func (m managedMemoryStub) Review(
	ctx context.Context,
	scope tool.Scope,
	lookup memory.Lookup,
) ([]memory.Card, error) {
	return m.review(ctx, scope, lookup)
}

func (m managedMemoryStub) Forget(
	ctx context.Context,
	scope tool.Scope,
	lookup memory.Lookup,
) (int, error) {
	return m.forget(ctx, scope, lookup)
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

func TestSuggestTipUsesSharedResponsePipelineAndPreservesRoute(t *testing.T) {
	wantCards := []memory.Card{{Title: "Study preference", Summary: "Prefers quiet study spaces."}}
	agent := &routeRecordingAgent{}
	store := profileMemoryStub{
		managedMemoryStub: managedMemoryStub{find: func(_ context.Context, _ tool.Scope, lookup memory.Lookup) ([]memory.Card, error) {
			if lookup.Query != "focus while studying" {
				t.Fatalf("lookup = %#v", lookup)
			}
			return wantCards, nil
		}},
		profile: func(context.Context, tool.Scope) (string, error) {
			return "User profile: studying for biology exams.", nil
		},
	}
	service := NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{
				Action:       ActionSuggestTip,
				Query:        "one focus tip",
				MemoryLookup: memory.Lookup{Query: "focus while studying"},
			}, nil
		}),
		agent,
		store,
		noConversation,
		noProposalConfirmation,
	)

	outcome, err := service.HandleUtterance(context.Background(), tool.Scope{UserID: "owner"}, "turn", "Any tip for staying focused?")
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Decision.Action != ActionSuggestTip || outcome.Response == "" ||
		agent.action != ActionSuggestTip || agent.query != "Any tip for staying focused?" ||
		agent.profile == "" || !reflect.DeepEqual(agent.memories, wantCards) {
		t.Fatalf("outcome=%#v agent=%#v", outcome, agent)
	}
}

func TestHandleUtteranceUsesActualProposalResult(t *testing.T) {
	service := NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{Action: ActionRespond, Query: "tomorrow after class"}, nil
		}),
		proposalAwareAgentFunc(func(
			context.Context,
			tool.Scope,
			string,
			session.Conversation,
			[]memory.Card,
		) (AgentResult, error) {
			return AgentResult{
				Text:            "What time does class end?",
				ProposalCreated: false,
			}, nil
		}),
		noMemories,
		noConversation,
		noProposalConfirmation,
	)

	outcome, err := service.HandleUtterance(
		context.Background(),
		tool.Scope{},
		"utterance-1",
		"I need to go tomorrow after class.",
	)
	if err != nil {
		t.Fatalf("HandleUtterance() error = %v", err)
	}
	if outcome.Response != "What time does class end?" || outcome.ProposalCreated {
		t.Fatalf("outcome = %#v", outcome)
	}
}

func TestHandleUtteranceRespondsToMeaningfulStateTransition(t *testing.T) {
	wantLookup := memory.Lookup{
		Query:  "The user just left the gym; suggest one timely next step.",
		Terms:  []string{"protein target"},
		Topics: []memory.Topic{memory.TopicHealth},
		Kinds:  []memory.Kind{memory.KindGoal},
	}
	wantCards := []memory.Card{{
		Topics:  []memory.Topic{memory.TopicHealth},
		Kind:    memory.KindGoal,
		Title:   "Daily protein target",
		Summary: "The user targets 150 grams of protein per day.",
	}}
	service := NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{
				Action:       ActionStateTransition,
				Query:        "The user just left the gym; suggest one timely next step.",
				MemoryLookup: wantLookup,
			}, nil
		}),
		agentFunc(func(
			_ context.Context,
			_ tool.Scope,
			query string,
			_ session.Conversation,
			cards []memory.Card,
		) (string, error) {
			if query != "I just left the gym." ||
				!reflect.DeepEqual(cards, wantCards) {
				t.Fatalf("Respond(%q, %#v)", query, cards)
			}
			return "Nice work. Grab a protein-forward meal next.", nil
		}),
		memoryReaderFunc(func(
			_ context.Context,
			_ tool.Scope,
			lookup memory.Lookup,
		) ([]memory.Card, error) {
			if !reflect.DeepEqual(lookup, wantLookup) {
				t.Fatalf("Find() lookup = %#v", lookup)
			}
			return wantCards, nil
		}),
		noConversation,
		noProposalConfirmation,
	)

	outcome, err := service.HandleUtterance(
		context.Background(),
		tool.Scope{UserID: "user-1", SessionID: "session-1"},
		"utterance-1",
		"I just left the gym.",
	)
	if err != nil {
		t.Fatalf("HandleUtterance() error = %v", err)
	}
	if outcome.Decision.Action != ActionStateTransition ||
		outcome.Response != "Nice work. Grab a protein-forward meal next." {
		t.Fatalf("outcome = %#v", outcome)
	}
}

func TestHandleUtteranceFallsBackAfterProposalResponseFailure(t *testing.T) {
	service := NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{Action: ActionRespond}, nil
		}),
		proposalAwareAgentFunc(func(
			context.Context,
			tool.Scope,
			string,
			session.Conversation,
			[]memory.Card,
		) (AgentResult, error) {
			return AgentResult{ProposalCreated: true, ProposalKinds: []ProposalKind{ProposalTask}}, errors.New("response unavailable")
		}),
		noMemories,
		noConversation,
		noProposalConfirmation,
	)

	outcome, err := service.HandleUtterance(
		context.Background(),
		tool.Scope{},
		"utterance-1",
		"Remind me tomorrow.",
	)
	if err != nil {
		t.Fatalf("HandleUtterance() error = %v", err)
	}
	if outcome.Response != proposalResponseFallback || !outcome.ProposalCreated ||
		!reflect.DeepEqual(outcome.ProposalKinds, []ProposalKind{ProposalTask}) {
		t.Fatalf("outcome = %#v", outcome)
	}
}

func TestHandleUtterancePreservesSpeechWithEnrichedRetrievalAndTrustedScope(t *testing.T) {
	t.Parallel()

	wantScope := tool.Scope{UserID: "user-123", SessionID: "session-456"}
	wantAgentScope := wantScope
	wantAgentScope.UtteranceID = "utterance-789"
	wantLookup := memory.Lookup{
		Query:  "What is nearby?",
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
	service := NewService(
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
			if query != "what's around here" {
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
			if scope != wantScope || utteranceID != "utterance-789" || text != "what's around here" {
				t.Fatalf("Prepare() scope = %#v, utterance ID = %q, text = %q", scope, utteranceID, text)
			}
			return wantConversation, nil
		}),
		noProposalConfirmation,
	)

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

func TestHandleUtterancePreparesConversationBeforeRoutingAndMemory(t *testing.T) {
	t.Parallel()

	started := make(chan string, 2)
	release := make(chan struct{})
	service := NewService(
		routerFunc(func(context.Context, string) (Decision, error) {
			return Decision{Action: ActionRespond, Query: "dinner ideas"}, nil
		}),
		agentFunc(func(
			context.Context,
			tool.Scope,
			string,
			session.Conversation,
			[]memory.Card,
		) (string, error) {
			return "Dinner.", nil
		}),
		memoryReaderFunc(func(
			context.Context,
			tool.Scope,
			memory.Lookup,
		) ([]memory.Card, error) {
			started <- "memory"
			<-release
			return nil, nil
		}),
		conversationReaderFunc(func(
			context.Context,
			tool.Scope,
			string,
			string,
		) (session.Conversation, error) {
			started <- "conversation"
			return session.Conversation{}, nil
		}),
		noProposalConfirmation,
	)

	done := make(chan error, 1)
	go func() {
		_, handleErr := service.HandleUtterance(
			context.Background(),
			tool.Scope{},
			"utterance-1",
			"What should I make?",
		)
		done <- handleErr
	}()

	seen := map[string]bool{}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for index := range 2 {
		select {
		case dependency := <-started:
			if index == 0 && dependency != "conversation" {
				t.Fatal("memory loaded before conversation preparation")
			}
			seen[dependency] = true
		case <-timer.C:
			close(release)
			t.Fatal("conversation preparation or memory lookup did not start")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("HandleUtterance() error = %v", err)
	}
	if !seen["memory"] || !seen["conversation"] {
		t.Fatalf("started = %#v", seen)
	}
}

func TestHandleUtteranceDoesNotCallAgentForNonResponseAction(t *testing.T) {
	t.Parallel()

	service := NewService(
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
			// Context is required before the router can choose state_update.
			return session.Conversation{}, nil
		}),
		noProposalConfirmation,
	)

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

	service := NewService(
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
	service := NewService(
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

	if _, err := service.HandleUtterance(context.Background(), tool.Scope{}, "utterance-1", "hello"); !errors.Is(err, routeErr) {
		t.Fatalf("HandleUtterance() error = %v, want wrapped route error", err)
	}

	agentErr := errors.New("agent failed")
	service = NewService(
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

	if _, err := service.HandleUtterance(context.Background(), tool.Scope{}, "utterance-1", "hello"); !errors.Is(err, agentErr) {
		t.Fatalf("HandleUtterance() error = %v, want wrapped agent error", err)
	}
}

func TestHandleUtteranceContinuesWhenMemoryLookupFails(t *testing.T) {
	t.Parallel()

	service := NewService(
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
	service := NewService(
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

func TestNewServiceRejectsIncompleteAssembly(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("incomplete assembly did not panic")
		}
	}()
	NewService(nil, nil, nil, nil, nil)
}
