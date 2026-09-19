package assistant

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/assistant/jev"
	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func TestJevToolClassifierSharesAllResponseContext(t *testing.T) {
	conversation := session.Conversation{
		Profile: "User profile: likes quiet places", Summary: "Looking for lunch near work",
		Messages: []session.Message{{ID: "private-transcript-id", Speaker: session.SpeakerUser, Text: "Under $20, please"}},
	}
	cards := []memory.Card{{Title: "Lunch preference", Summary: "Prefers vegetarian food"}}
	turn, err := NewResponseContext(tool.Scope{UserID: "private-user-id", TimeZone: "America/Los_Angeles", AlwaysRespond: true}, "Find one nearby", conversation, cards, time.Date(2026, 9, 19, 18, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	state := ToolState{Context: turn, Results: []ToolObservation{{Name: "get_current_location", Content: "Las Vegas"}}}
	evaluator := &fakeJevEvaluator{response: jev.Response{Answers: map[string]jev.Answer{
		"search_web":   {Type: jev.QuestionNoul, Noul: .95},
		"propose_task": {Type: jev.QuestionNoul, Noul: .05},
	}}}
	classifier, err := NewJevToolClassifier(evaluator)
	if err != nil {
		t.Fatal(err)
	}
	got, err := classifier.Select(context.Background(), state, []tool.Spec{{Name: "search_web"}, {Name: "propose_task"}})
	if err != nil || !reflect.DeepEqual(got, []string{"search_web"}) {
		t.Fatalf("selection=%v err=%v", got, err)
	}
	if !reflect.DeepEqual(evaluator.request.State, state) {
		t.Fatal("classifier did not receive the complete response context")
	}
	if turn.CurrentLocalTime != "2026-09-19T11:00:00-07:00" || !reflect.DeepEqual(turn.Memories, cards) || turn.Profile != conversation.Profile || turn.Summary != conversation.Summary || turn.Messages[0].Text != conversation.Messages[0].Text {
		t.Fatalf("turn=%+v", turn)
	}
	for _, question := range evaluator.request.Questions {
		if question.Type != jev.QuestionNoul || !strings.Contains(question.Instructions.(string), "context.query") {
			t.Fatalf("question=%+v", question)
		}
	}
}

func TestJevToolClassifierCanChooseManyOrNone(t *testing.T) {
	for _, probabilities := range [][2]float64{{.99, .9}, {.1, .2}, {.5, .5}} {
		evaluator := &fakeJevEvaluator{response: jev.Response{Answers: map[string]jev.Answer{
			"get_current_location": {Type: jev.QuestionNoul, Noul: probabilities[0]},
			"search_web":           {Type: jev.QuestionNoul, Noul: probabilities[1]},
		}}}
		classifier, _ := NewJevToolClassifier(evaluator)
		selected, err := classifier.Select(context.Background(), ToolState{}, []tool.Spec{{Name: "get_current_location"}, {Name: "search_web"}})
		want := 0
		if probabilities[0] > .5 {
			want = 2
		}
		if err != nil || len(selected) != want || evaluator.calls != 1 {
			t.Fatalf("selected=%v err=%v calls=%d", selected, err, evaluator.calls)
		}
	}
}

func TestJevToolClassifierRejectsInvalidAnswers(t *testing.T) {
	for _, answers := range []map[string]jev.Answer{
		{}, {"search_web": {Type: jev.QuestionChoice, Choice: "yes"}},
		{"search_web": {Type: jev.QuestionNoul, Noul: 1.1}},
		{"search_web": {Type: jev.QuestionNoul, Noul: math.NaN()}},
		{"search_web": {Type: jev.QuestionNoul, Noul: .9}, "unknown": {}},
	} {
		classifier, _ := NewJevToolClassifier(&fakeJevEvaluator{response: jev.Response{Answers: answers}})
		if _, err := classifier.Select(context.Background(), ToolState{}, []tool.Spec{{Name: "search_web"}}); err == nil {
			t.Fatalf("accepted invalid answers: %+v", answers)
		}
	}
	evaluator := &fakeJevEvaluator{err: errors.New("unavailable")}
	classifier, _ := NewJevToolClassifier(evaluator)
	if _, err := classifier.Select(context.Background(), ToolState{}, []tool.Spec{{Name: "search_web"}}); err == nil {
		t.Fatal("ignored service failure")
	}
	if _, err := classifier.Select(context.Background(), ToolState{Context: ResponseContext{MemoryReview: true}}, []tool.Spec{{Name: "search_web"}}); err != nil || evaluator.calls != 1 {
		t.Fatal("memory review called Jev")
	}
}
