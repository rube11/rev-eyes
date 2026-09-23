package assistant

import (
	"context"
	"encoding/json"
	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/speech"
	"github.com/rube11/rev-eyes/backend/internal/tool"
	"testing"
	"time"
)

func otherSpeech() *speech.Utterance {
	return &speech.Utterance{Text: "Yes, remember that and remind me tomorrow.", Segments: []speech.Segment{{Role: speech.Other, Text: "Yes, remember that and remind me tomorrow."}}}
}

func TestOtherSpeechCannotConfirmOrSelectAccountRoutes(t *testing.T) {
	for _, action := range []Action{ActionRemember, ActionMemoryCorrect, ActionMemoryForget, ActionProfileInclude, ActionProfileExclude, ActionMemoryReview, ActionRespond, ActionResolveProposal, ActionStateTransition} {
		t.Run(string(action), func(t *testing.T) {
			s := NewService(routerFunc(func(context.Context, string) (Decision, error) { return Decision{Action: action}, nil }),
				agentFunc(func(context.Context, tool.Scope, string, session.Conversation, []memory.Card) (string, error) {
					t.Error("disallowed route reached composer")
					return "", nil
				}),
				managedMemoryStub{forget: func(context.Context, tool.Scope, memory.Lookup) (int, error) {
					t.Error("memory mutation")
					return 0, nil
				}}, noConversation,
				proposalConfirmerFunc(func(context.Context, tool.Scope, string) (string, bool, error) {
					t.Error("other speaker confirmed a proposal")
					return "", false, nil
				}))
			out, err := s.HandleUtterance(context.Background(), tool.Scope{Speech: otherSpeech()}, "turn", "yes")
			if err != nil || out.Decision.Action != ActionIgnore {
				t.Fatalf("%+v %v", out, err)
			}
		})
	}
}

func TestOtherSpeechTipKeepsAttributionAndPersonalContext(t *testing.T) {
	speechInput := otherSpeech()
	evaluator := jevEvaluatorReturning(ActionSuggestTip)
	router, _ := NewJevRouter(evaluator)
	called := false
	s := NewService(router, agentFunc(func(_ context.Context, scope tool.Scope, query string, conversation session.Conversation, cards []memory.Card) (string, error) {
		called = true
		if scope.Speech != speechInput || conversation.Speech != speechInput || query != speechInput.Text || conversation.Profile != "Wearer prefers concise suggestions" || len(cards) != 1 {
			t.Errorf("lost context: %+v %+v", scope, conversation)
		}
		turn, err := NewResponseContext(scope, query, conversation, cards, time.Now())
		if err != nil || turn.Speech != speechInput {
			t.Error("tool/composer context lost speakers")
		}
		return "Ask which deadline matters most.", nil
	}), profileMemoryStub{managedMemoryStub: managedMemoryStub{find: func(context.Context, tool.Scope, memory.Lookup) ([]memory.Card, error) {
		return []memory.Card{{Title: "Project context"}}, nil
	}}, profile: func(context.Context, tool.Scope) (string, error) { return "Wearer prefers concise suggestions", nil }}, noConversation,
		proposalConfirmerFunc(func(context.Context, tool.Scope, string) (string, bool, error) {
			t.Error("unexpected confirmation")
			return "", false, nil
		}))
	out, err := s.HandleUtterance(context.Background(), tool.Scope{Speech: speechInput}, "turn", speechInput.Text)
	if err != nil || !called || out.Decision.Action != ActionSuggestTip {
		t.Fatalf("%+v %v", out, err)
	}
	state := evaluator.request.State.(jevState)
	if state.Speech != speechInput {
		t.Fatal("router did not receive speaker attribution")
	}
	criteria := evaluator.request.Questions[jevRouteQuestion].Criteria.(map[string]any)
	if len(criteria) != 3 || criteria[string(ActionSuggestTip)] == nil || criteria[string(ActionRespond)] != nil {
		t.Fatalf("unsafe routes: %+v", criteria)
	}
}

func TestOtherSpeechToolsAllowEvidenceButNotProposals(t *testing.T) {
	scope := tool.Scope{Speech: otherSpeech()}
	for _, malicious := range []bool{false, true} {
		executed := 0
		w := workflowForTest(t, []workflowTool{
			{name: "search_web", execute: func(context.Context, tool.Scope, json.RawMessage) (tool.Result, error) {
				executed++
				return tool.Result{Content: "evidence"}, nil
			}},
			{name: "propose_task", mutating: true, execute: func(context.Context, tool.Scope, json.RawMessage) (tool.Result, error) {
				t.Error("proposal executed")
				return tool.Result{}, nil
			}},
		}, func(_ context.Context, _ ToolState, specs []tool.Spec) ([]string, error) {
			for _, spec := range specs {
				if !spec.ReadOnly {
					t.Error("mutating spec offered")
				}
			}
			if malicious {
				return []string{"propose_task"}, nil
			}
			return []string{"search_web"}, nil
		}, func(context.Context, ToolState, []tool.Spec) (map[string]PreparedToolArguments, error) {
			return map[string]PreparedToolArguments{"search_web": {Arguments: json.RawMessage(`{}`)}}, nil
		})
		_, err := w.Run(context.Background(), scope, ResponseContext{Speech: scope.Speech})
		if (err != nil) != malicious || (!malicious && executed != 1) {
			t.Fatalf("malicious=%v executed=%d error=%v", malicious, executed, err)
		}
		if _, err := w.registry.Execute(context.Background(), scope, "propose_task", nil); err == nil {
			t.Fatal("registry bypassed speaker policy")
		}
	}
}
