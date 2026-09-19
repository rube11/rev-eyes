package assistant

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/assistant/jev"
	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
)

type fakeJevEvaluator struct {
	response jev.Response
	err      error
	request  jev.Request
	calls    int
}

func (f *fakeJevEvaluator) Evaluate(_ context.Context, request jev.Request) (jev.Response, error) {
	f.calls++
	f.request = request
	return f.response, f.err
}

type fakeDecisionEnricher struct {
	decision     Decision
	err          error
	action       Action
	utterance    string
	conversation session.Conversation
	calls        int
}

func (f *fakeDecisionEnricher) Enrich(
	_ context.Context,
	action Action,
	utterance string,
	conversation session.Conversation,
) (Decision, error) {
	f.calls++
	f.action = action
	f.utterance = utterance
	f.conversation = conversation
	return f.decision, f.err
}

func TestJevRouterKeepsSilentRoutesLocal(t *testing.T) {
	for _, action := range []Action{ActionIgnore, ActionStateUpdate} {
		t.Run(string(action), func(t *testing.T) {
			evaluator := jevEvaluatorReturning(action)
			enricher := &fakeDecisionEnricher{}
			router, err := NewJevRouter(evaluator, enricher)
			if err != nil {
				t.Fatalf("NewJevRouter: %v", err)
			}

			got, err := router.Route(context.Background(), " Current context ")
			if err != nil {
				t.Fatalf("Route: %v", err)
			}
			want := Decision{Action: action}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("decision = %#v, want %#v", got, want)
			}
			if enricher.calls != 0 {
				t.Fatalf("enricher calls = %d, want 0", enricher.calls)
			}
		})
	}
}

func TestJevRouterOwnsActionAndEnrichesMemoryLookup(t *testing.T) {
	evaluator := jevEvaluatorReturning(ActionRespond)
	enricher := &fakeDecisionEnricher{decision: Decision{
		Action: ActionMemoryForget,
		Query:  " post workout food ",
		MemoryLookup: memory.Lookup{
			Terms: []string{" Protein Target ", "protein target"},
		},
	}}
	router, err := NewJevRouter(evaluator, enricher)
	if err != nil {
		t.Fatalf("NewJevRouter: %v", err)
	}

	got, err := router.Route(context.Background(), " What should I eat? ")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if got.Action != ActionRespond {
		t.Fatalf("action = %q, want Jev action %q", got.Action, ActionRespond)
	}
	if got.Query != "post workout food" {
		t.Errorf("query = %q", got.Query)
	}
	if !reflect.DeepEqual(got.MemoryLookup.Terms, []string{"protein target"}) {
		t.Errorf("lookup terms = %#v", got.MemoryLookup.Terms)
	}
	if enricher.action != ActionRespond {
		t.Errorf("enricher action = %q, want %q", enricher.action, ActionRespond)
	}
	if enricher.utterance != "What should I eat?" {
		t.Errorf("enricher utterance = %q", enricher.utterance)
	}
}

func TestJevRouterClassifiesExactSpecializedActionWithContext(t *testing.T) {
	evaluator := jevEvaluatorReturning(ActionMemoryReview)
	want := Decision{Action: ActionMemoryReview, Query: "Jolene"}
	enricher := &fakeDecisionEnricher{decision: want}
	router, err := NewJevRouter(evaluator, enricher)
	if err != nil {
		t.Fatalf("NewJevRouter: %v", err)
	}
	conversation := session.Conversation{Messages: []session.Message{
		{Speaker: session.SpeakerUser, Text: "What do you know about my team?"},
	}}

	got, err := router.RouteWithContext(context.Background(), "What about Jolene?", conversation)
	if err != nil {
		t.Fatalf("RouteWithContext: %v", err)
	}
	if got.Action != want.Action || got.Query != want.Query {
		t.Fatalf("decision = %#v, want action %q and query %q", got, want.Action, want.Query)
	}
	if !reflect.DeepEqual(enricher.conversation, conversation) {
		t.Fatalf("conversation = %#v, want %#v", enricher.conversation, conversation)
	}

	state, ok := evaluator.request.State.(jevState)
	if !ok {
		t.Fatalf("state type = %T, want jevState", evaluator.request.State)
	}
	if state.LatestUtterance != "What about Jolene?" || len(state.RecentDialogue) != 1 {
		t.Fatalf("state = %#v", state)
	}
	question := evaluator.request.Questions[jevRouteQuestion]
	criteria, ok := question.Criteria.(map[string]any)
	if !ok || len(criteria) != 10 {
		t.Fatalf("route criteria = %#v", question.Criteria)
	}
	if _, exists := criteria["specialized_action"]; exists {
		t.Fatal("Jev criteria still contain specialized_action")
	}
	if _, exists := criteria[string(ActionProposeTask)]; exists {
		t.Fatal("top-level router still owns reminder tool intent")
	}
	if _, exists := criteria[string(ActionProposeWatch)]; exists {
		t.Fatal("top-level router still owns watch tool intent")
	}
}

func TestJevRouterRejectsUnknownRoute(t *testing.T) {
	for _, action := range []Action{"specialized_action", ActionProposeTask, ActionProposeWatch} {
		evaluator := &fakeJevEvaluator{response: jev.Response{Answers: map[string]jev.Answer{
			jevRouteQuestion: {Choice: string(action)},
		}}}
		router, err := NewJevRouter(evaluator, &fakeDecisionEnricher{})
		if err != nil {
			t.Fatalf("NewJevRouter: %v", err)
		}
		if _, err := router.Route(context.Background(), "remember this"); err == nil {
			t.Fatalf("Route(%q) error = nil, want unsupported route error", action)
		}
	}
}

func TestJevRouterReturnsEvaluationErrors(t *testing.T) {
	evaluator := &fakeJevEvaluator{err: errors.New("unavailable")}
	router, err := NewJevRouter(evaluator, &fakeDecisionEnricher{})
	if err != nil {
		t.Fatalf("NewJevRouter: %v", err)
	}
	if _, err := router.Route(context.Background(), "hello"); err == nil {
		t.Fatal("Route error = nil, want evaluation error")
	}
}

func TestJevRouterKeepsRuleBasedFillerLocal(t *testing.T) {
	evaluator := &fakeJevEvaluator{}
	router, err := NewJevRouter(evaluator, &fakeDecisionEnricher{})
	if err != nil {
		t.Fatalf("NewJevRouter: %v", err)
	}
	decision, err := router.Route(context.Background(), "um")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if decision.Action != ActionIgnore || evaluator.calls != 0 {
		t.Fatalf("decision = %#v, evaluator calls = %d", decision, evaluator.calls)
	}
}

func jevEvaluatorReturning(action Action) *fakeJevEvaluator {
	return &fakeJevEvaluator{response: jev.Response{Answers: map[string]jev.Answer{
		jevRouteQuestion: {
			Type:       jev.QuestionChoice,
			Choice:     string(action),
			Confidence: 0.9,
		},
	}}}
}
