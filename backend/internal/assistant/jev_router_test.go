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

func TestJevRouterKeepsSilentRoutesLocal(t *testing.T) {
	for _, action := range []Action{ActionIgnore, ActionStateUpdate} {
		t.Run(string(action), func(t *testing.T) {
			evaluator := jevEvaluatorReturning(action)
			router, err := NewJevRouter(evaluator)
			if err != nil {
				t.Fatalf("NewJevRouter: %v", err)
			}

			got, err := router.Route(context.Background(), " Current context ")
			if err != nil {
				t.Fatalf("Route: %v", err)
			}
			if want := (Decision{Action: action}); !reflect.DeepEqual(got, want) {
				t.Fatalf("decision = %#v, want %#v", got, want)
			}
			if evaluator.calls != 1 {
				t.Fatalf("Jev calls = %d, want 1", evaluator.calls)
			}
		})
	}
}

func TestJevRouterBuildsMemoryLookupFromSameEvaluation(t *testing.T) {
	evaluator := jevEvaluatorReturning(ActionRespond)
	evaluator.response.Answers[memoryTopicQuestion(string(memory.TopicHealth))] = jev.Answer{Noul: 0.91}
	evaluator.response.Answers[memoryTopicQuestion(string(memory.TopicPreferences))] = jev.Answer{Noul: 0.82}
	evaluator.response.Answers[memoryTopicQuestion(string(memory.TopicOther))] = jev.Answer{Noul: 0.12}
	evaluator.response.Answers[memoryKindQuestion(string(memory.KindPreference))] = jev.Answer{Noul: 0.88}
	evaluator.response.Answers[memoryKindQuestion(string(memory.KindGoal))] = jev.Answer{Noul: 0.69}
	router, err := NewJevRouter(evaluator)
	if err != nil {
		t.Fatal(err)
	}

	got, err := router.Route(context.Background(), " What should I eat? ")
	if err != nil {
		t.Fatal(err)
	}
	wantTopics := []memory.Topic{memory.TopicHealth, memory.TopicPreferences}
	wantKinds := []memory.Kind{memory.KindPreference, memory.KindGoal}
	if got.Action != ActionRespond || got.Query != "What should I eat?" || got.MemoryLookup.Query != "What should I eat?" {
		t.Fatalf("decision = %#v", got)
	}
	if !reflect.DeepEqual(got.MemoryLookup.Topics, wantTopics) || !reflect.DeepEqual(got.MemoryLookup.Kinds, wantKinds) {
		t.Fatalf("lookup = %#v, want topics=%#v kinds=%#v", got.MemoryLookup, wantTopics, wantKinds)
	}
	if evaluator.calls != 1 {
		t.Fatalf("Jev calls = %d, want 1", evaluator.calls)
	}
}

func TestJevRouterSuppliesConversationToEveryJudgment(t *testing.T) {
	evaluator := jevEvaluatorReturning(ActionMemoryReview)
	evaluator.response.Answers[jevMemoryReviewAllQuestion] = jev.Answer{Noul: 0.92}
	router, err := NewJevRouter(evaluator)
	if err != nil {
		t.Fatal(err)
	}
	conversation := session.Conversation{Messages: []session.Message{{
		Speaker: session.SpeakerUser, Text: "What do you know about my team?",
	}}}

	decision, err := router.RouteWithContext(context.Background(), "What do you know about me?", conversation)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != ActionMemoryReview || !decision.MemoryReviewAll || decision.Query != "" {
		t.Fatalf("decision = %#v", decision)
	}
	state, ok := evaluator.request.State.(jevState)
	if !ok || state.LatestUtterance != "What do you know about me?" || len(state.RecentDialogue) != 1 {
		t.Fatalf("state = %#v", evaluator.request.State)
	}
	questions := evaluator.request.Questions
	if len(questions) != 1+1+len(memory.TopicValues())+len(memory.KindValues()) {
		t.Fatalf("question count = %d", len(questions))
	}
	criteria, ok := questions[jevRouteQuestion].Criteria.(map[string]any)
	if !ok || len(criteria) != 11 {
		t.Fatalf("route criteria = %#v", questions[jevRouteQuestion].Criteria)
	}
	if _, exists := criteria[string(ActionSuggestTip)]; !exists {
		t.Fatal("Jev criteria omit suggest_tip")
	}
	if questions[memoryTopicQuestion(string(memory.TopicHealth))].Type != jev.QuestionNoul {
		t.Fatal("memory topic is not a Jev Noul")
	}
}

func TestJevRouterRoutesSuggestTipWithMemorySignals(t *testing.T) {
	evaluator := jevEvaluatorReturning(ActionSuggestTip)
	evaluator.response.Answers[memoryTopicQuestion(string(memory.TopicGoals))] = jev.Answer{Noul: 0.94}
	evaluator.response.Answers[memoryKindQuestion(string(memory.KindGoal))] = jev.Answer{Noul: 0.9}
	router, err := NewJevRouter(evaluator)
	if err != nil {
		t.Fatal(err)
	}

	decision, err := router.Route(context.Background(), "Any tip for staying focused?")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != ActionSuggestTip || !reflect.DeepEqual(decision.MemoryLookup.Topics, []memory.Topic{memory.TopicGoals}) {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestJevRouterLeavesIncompleteMemoryReferencesForCodeOwnedFallbacks(t *testing.T) {
	for _, test := range []struct {
		action    Action
		utterance string
	}{
		{ActionMemoryCorrect, "That's wrong."},
		{ActionMemoryForget, "Forget that."},
		{ActionProfileInclude, "Always keep that in my profile."},
		{ActionProfileExclude, "Remove this from my profile."},
	} {
		t.Run(string(test.action), func(t *testing.T) {
			evaluator := jevEvaluatorReturning(test.action)
			evaluator.response.Answers[memoryTopicQuestion(string(memory.TopicPersonal))] = jev.Answer{Noul: 0.99}
			evaluator.response.Answers[memoryKindQuestion(string(memory.KindFact))] = jev.Answer{Noul: 0.99}
			router, err := NewJevRouter(evaluator)
			if err != nil {
				t.Fatal(err)
			}
			decision, err := router.Route(context.Background(), test.utterance)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Query != "" || !decision.MemoryLookup.Empty() ||
				len(decision.MemoryLookup.Topics) != 0 || len(decision.MemoryLookup.Kinds) != 0 {
				t.Fatalf("incomplete reference became a lookup: %#v", decision)
			}
		})
	}
}

func TestJevRouterKeepsCompleteMemoryReplacement(t *testing.T) {
	evaluator := jevEvaluatorReturning(ActionMemoryCorrect)
	router, err := NewJevRouter(evaluator)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := router.Route(context.Background(), "Change my protein target to 150 grams.")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Query != "Change my protein target to 150 grams." {
		t.Fatalf("query = %q", decision.Query)
	}
}

func TestJevRouterRejectsUnknownRoute(t *testing.T) {
	for _, action := range []Action{"specialized_action", "propose_task", "propose_watch"} {
		evaluator := jevEvaluatorReturning(action)
		router, err := NewJevRouter(evaluator)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := router.Route(context.Background(), "remember this"); err == nil {
			t.Fatalf("Route(%q) error = nil, want unsupported route error", action)
		}
	}
}

func TestJevRouterReturnsEvaluationErrors(t *testing.T) {
	evaluator := &fakeJevEvaluator{err: errors.New("unavailable")}
	router, err := NewJevRouter(evaluator)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := router.Route(context.Background(), "hello"); err == nil {
		t.Fatal("Route error = nil, want evaluation error")
	}
}

func TestJevRouterKeepsRuleBasedFillerLocal(t *testing.T) {
	evaluator := &fakeJevEvaluator{}
	router, err := NewJevRouter(evaluator)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := router.Route(context.Background(), "um")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != ActionIgnore || evaluator.calls != 0 {
		t.Fatalf("decision = %#v, evaluator calls = %d", decision, evaluator.calls)
	}
}

func jevEvaluatorReturning(action Action) *fakeJevEvaluator {
	return &fakeJevEvaluator{response: jev.Response{Answers: map[string]jev.Answer{
		jevRouteQuestion: {Type: jev.QuestionChoice, Choice: string(action), Confidence: 0.9},
	}}}
}
