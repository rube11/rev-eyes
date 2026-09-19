package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type toolClassifierFunc func(context.Context, ToolState, []tool.Spec) ([]string, error)

func (f toolClassifierFunc) Select(ctx context.Context, state ToolState, specs []tool.Spec) ([]string, error) {
	return f(ctx, state, specs)
}

type toolArgumentsFunc func(context.Context, ToolState, []tool.Spec) (map[string]PreparedToolArguments, error)

func (f toolArgumentsFunc) Build(ctx context.Context, state ToolState, specs []tool.Spec) (map[string]PreparedToolArguments, error) {
	return f(ctx, state, specs)
}

type workflowTool struct {
	name     string
	mutating bool
	execute  func(context.Context, tool.Scope, json.RawMessage) (tool.Result, error)
}

func (t workflowTool) Spec() tool.Spec {
	return tool.Spec{Name: t.name, ReadOnly: !t.mutating, Parameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}
}
func (t workflowTool) Execute(ctx context.Context, scope tool.Scope, args json.RawMessage) (tool.Result, error) {
	return t.execute(ctx, scope, args)
}

func workflowForTest(t *testing.T, tools []workflowTool, classifier toolClassifierFunc, builder toolArgumentsFunc) *ToolWorkflow {
	t.Helper()
	registry := tool.NewRegistry()
	for _, registered := range tools {
		if err := registry.Register(registered); err != nil {
			t.Fatal(err)
		}
	}
	w, err := NewToolWorkflow(registry, classifier, builder)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestToolWorkflowLocationFeedsDependentSearch(t *testing.T) {
	for _, locationFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "located", true: "location unavailable"}[locationFails], func(t *testing.T) {
			scope := tool.Scope{UserID: "user", SessionID: "session", UtteranceID: "utterance"}
			turn := ResponseContext{Query: "Find a cafe nearby", Profile: "User profile: quiet places"}
			locationCalls, searchCalls := 0, 0
			w := workflowForTest(t, []workflowTool{
				{name: "get_current_location", execute: func(_ context.Context, got tool.Scope, args json.RawMessage) (tool.Result, error) {
					locationCalls++
					if got != scope || string(args) != "{}" {
						t.Fatalf("scope=%+v args=%s", got, args)
					}
					if locationFails {
						return tool.Result{}, errors.New("location unavailable")
					}
					return tool.Result{Content: `{"city":"Las Vegas"}`}, nil
				}},
				{name: "search_web", execute: func(_ context.Context, got tool.Scope, args json.RawMessage) (tool.Result, error) {
					searchCalls++
					if got != scope || !strings.Contains(string(args), "Las Vegas") {
						t.Errorf("scope=%+v args=%s", got, args)
					}
					return tool.Result{Content: "Cafe evidence"}, nil
				}},
			}, func(_ context.Context, state ToolState, _ []tool.Spec) ([]string, error) {
				if !reflect.DeepEqual(state.Context, turn) {
					t.Fatal("context changed")
				}
				if len(state.Results) > 0 {
					if len(state.Results) == 1 && !locationFails {
						return []string{"search_web"}, nil
					}
					return nil, nil
				}
				return []string{"search_web", "get_current_location"}, nil
			}, func(_ context.Context, state ToolState, specs []tool.Spec) (map[string]PreparedToolArguments, error) {
				if locationCalls != 1 || len(state.Results) != 1 || len(specs) != 1 || specs[0].Name != "search_web" {
					t.Fatalf("state=%+v specs=%+v", state, specs)
				}
				if locationFails {
					t.Fatal("builder should not decide readiness after location failure")
				}
				return map[string]PreparedToolArguments{"search_web": {Arguments: json.RawMessage(`{"query":"quiet cafes Las Vegas"}`)}}, nil
			})
			result, err := w.Run(context.Background(), scope, turn)
			wantSearch := 1
			wantResults := 2
			if locationFails {
				wantSearch = 0
				wantResults = 1
			}
			if err != nil || len(result.Results) != wantResults || searchCalls != wantSearch {
				t.Fatalf("result=%+v searches=%d err=%v", result, searchCalls, err)
			}
		})
	}
}

func TestToolWorkflowBoundsSearchAndNeverRepeatsProposal(t *testing.T) {
	searchCalls, proposalCalls, selections := 0, 0, 0
	w := workflowForTest(t, []workflowTool{
		{name: "search_web", execute: func(context.Context, tool.Scope, json.RawMessage) (tool.Result, error) {
			searchCalls++
			return tool.Result{}, errors.New("no evidence")
		}},
		{name: "propose_task", mutating: true, execute: func(context.Context, tool.Scope, json.RawMessage) (tool.Result, error) {
			proposalCalls++
			return tool.Result{Content: `{"status":"proposed"}`}, nil
		}},
	}, func(_ context.Context, state ToolState, specs []tool.Spec) ([]string, error) {
		selections++
		var names []string
		for _, spec := range specs {
			names = append(names, spec.Name)
		}
		return names, nil
	}, func(_ context.Context, state ToolState, specs []tool.Spec) (map[string]PreparedToolArguments, error) {
		args := map[string]PreparedToolArguments{}
		for _, spec := range specs {
			query := fmt.Sprintf(`{"query":"attempt %d"}`, len(state.Results))
			args[spec.Name] = PreparedToolArguments{Arguments: json.RawMessage(query)}
		}
		return args, nil
	})
	result, err := w.Run(context.Background(), tool.Scope{}, ResponseContext{Query: "Find the price and remind me tomorrow"})
	if err != nil || searchCalls != 1 || proposalCalls != 1 || selections != 2 || !result.ProposalCreated {
		t.Fatalf("result=%+v searches=%d proposals=%d selections=%d err=%v", result, searchCalls, proposalCalls, selections, err)
	}
}

func TestToolWorkflowRejectsInvalidSelectionsAndArgumentsBeforeEffects(t *testing.T) {
	for _, scenario := range []string{"unknown tool", "duplicate tool", "extra arguments", "missing arguments", "null arguments", "not object"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			w := workflowForTest(t, []workflowTool{{name: "propose_task", mutating: true, execute: func(context.Context, tool.Scope, json.RawMessage) (tool.Result, error) {
				calls++
				return tool.Result{}, nil
			}}},
				func(context.Context, ToolState, []tool.Spec) ([]string, error) {
					switch scenario {
					case "unknown tool":
						return []string{"unregistered"}, nil
					case "duplicate tool":
						return []string{"propose_task", "propose_task"}, nil
					}
					return []string{"propose_task"}, nil
				}, func(context.Context, ToolState, []tool.Spec) (map[string]PreparedToolArguments, error) {
					args := map[string]PreparedToolArguments{"propose_task": {Arguments: json.RawMessage(`{}`)}}
					switch scenario {
					case "extra arguments":
						args["extra"] = PreparedToolArguments{}
					case "missing arguments":
						delete(args, "propose_task")
					case "null arguments":
						args["propose_task"] = PreparedToolArguments{Arguments: json.RawMessage(`null`)}
					case "not object":
						args["propose_task"] = PreparedToolArguments{Arguments: json.RawMessage(`[]`)}
					}
					return args, nil
				})
			if _, err := w.Run(context.Background(), tool.Scope{}, ResponseContext{}); err == nil || calls != 0 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestToolWorkflowReadOnlyBatchRunsConcurrentlyBeforeMutations(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	read := func(ctx context.Context, _ tool.Scope, _ json.RawMessage) (tool.Result, error) {
		started <- struct{}{}
		select {
		case <-release:
			return tool.Result{Content: "done"}, nil
		case <-ctx.Done():
			return tool.Result{}, ctx.Err()
		}
	}
	mutated := false
	w := workflowForTest(t, []workflowTool{{name: "a", execute: read}, {name: "b", execute: read}, {name: "propose_task", mutating: true, execute: func(context.Context, tool.Scope, json.RawMessage) (tool.Result, error) {
		mutated = true
		return tool.Result{Content: `{"status":"proposed"}`}, nil
	}}},
		func(_ context.Context, _ ToolState, specs []tool.Spec) ([]string, error) {
			var names []string
			for _, spec := range specs {
				names = append(names, spec.Name)
			}
			return names, nil
		},
		func(_ context.Context, state ToolState, specs []tool.Spec) (map[string]PreparedToolArguments, error) {
			args := map[string]PreparedToolArguments{}
			for _, spec := range specs {
				if !spec.ReadOnly && len(state.Results) != 2 {
					t.Error("proposal prepared before evidence")
				}
				args[spec.Name] = PreparedToolArguments{Arguments: json.RawMessage(`{}`)}
			}
			return args, nil
		})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := w.Run(ctx, tool.Scope{}, ResponseContext{}); done <- err }()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("read-only tools did not start concurrently")
		}
	}
	if mutated {
		t.Fatal("mutation ran before reads completed")
	}
	close(release)
	if err := <-done; err != nil || !mutated {
		t.Fatalf("mutated=%v err=%v", mutated, err)
	}
}

func TestToolWorkflowNoToolsAndReviewSkipArgumentBuilder(t *testing.T) {
	selected := 0
	w := workflowForTest(t, []workflowTool{{name: "search_web"}}, func(context.Context, ToolState, []tool.Spec) ([]string, error) { selected++; return nil, nil }, func(context.Context, ToolState, []tool.Spec) (map[string]PreparedToolArguments, error) {
		t.Fatal("unused builder called")
		return nil, nil
	})
	if _, err := w.Run(context.Background(), tool.Scope{}, ResponseContext{}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Run(context.Background(), tool.Scope{MemoryReview: true}, ResponseContext{}); err != nil {
		t.Fatal(err)
	}
	if selected != 1 {
		t.Fatalf("selection calls=%d", selected)
	}
}

func TestToolWorkflowPreservesProposalWhenRetryClassificationFails(t *testing.T) {
	w := workflowForTest(t, []workflowTool{
		{name: "propose_task", mutating: true, execute: func(context.Context, tool.Scope, json.RawMessage) (tool.Result, error) {
			return tool.Result{Content: `{"status":"proposed"}`}, nil
		}},
		{name: "search_web", execute: func(context.Context, tool.Scope, json.RawMessage) (tool.Result, error) {
			return tool.Result{}, errors.New("failed")
		}},
	}, func(_ context.Context, state ToolState, _ []tool.Spec) ([]string, error) {
		if len(state.Results) > 0 {
			return nil, errors.New("Jev unavailable")
		}
		return []string{"propose_task"}, nil
	},
		func(context.Context, ToolState, []tool.Spec) (map[string]PreparedToolArguments, error) {
			return map[string]PreparedToolArguments{"propose_task": {Arguments: json.RawMessage(`{}`)}}, nil
		})
	result, err := w.Run(context.Background(), tool.Scope{}, ResponseContext{})
	if err == nil || !result.ProposalCreated {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestToolWorkflowNeverRetriesFailedSearch(t *testing.T) {
	calls, builds := 0, 0
	w := workflowForTest(t, []workflowTool{{name: "search_web", execute: func(context.Context, tool.Scope, json.RawMessage) (tool.Result, error) {
		calls++
		return tool.Result{}, errors.New("unavailable")
	}}}, func(context.Context, ToolState, []tool.Spec) ([]string, error) {
		return []string{"search_web"}, nil
	}, func(context.Context, ToolState, []tool.Spec) (map[string]PreparedToolArguments, error) {
		builds++
		args := `{"query":"same","mode":"research"}`
		return map[string]PreparedToolArguments{"search_web": {Arguments: json.RawMessage(args)}}, nil
	})
	result, err := w.Run(context.Background(), tool.Scope{}, ResponseContext{})
	if err != nil || calls != 1 || builds != 1 || len(result.Results) != 1 || !strings.Contains(result.Results[0].Error, "unavailable") {
		t.Fatalf("calls=%d builds=%d result=%+v err=%v", calls, builds, result, err)
	}
}

func TestToolWorkflowCancellationPreventsFurtherEffectsAndPreservesProposal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := workflowForTest(t, []workflowTool{
		{name: "propose_task", mutating: true, execute: func(context.Context, tool.Scope, json.RawMessage) (tool.Result, error) {
			cancel()
			return tool.Result{Content: `{"status":"proposed"}`}, nil
		}},
		{name: "propose_watch", mutating: true, execute: func(context.Context, tool.Scope, json.RawMessage) (tool.Result, error) {
			t.Fatal("executed a second proposal after cancellation")
			return tool.Result{}, nil
		}},
	}, func(context.Context, ToolState, []tool.Spec) ([]string, error) {
		return []string{"propose_task", "propose_watch"}, nil
	}, func(context.Context, ToolState, []tool.Spec) (map[string]PreparedToolArguments, error) {
		return map[string]PreparedToolArguments{"propose_task": {Arguments: json.RawMessage(`{}`)}, "propose_watch": {Arguments: json.RawMessage(`{}`)}}, nil
	})
	result, err := w.Run(ctx, tool.Scope{}, ResponseContext{})
	if !errors.Is(err, context.Canceled) || !result.ProposalCreated || len(result.Results) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestToolWorkflowReconsidersProposalAfterSearch(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprintf("canceled=%v", canceled), func(t *testing.T) {
			proposals, selections := 0, 0
			evidence := "Museum opens tomorrow at 10 AM"
			if canceled {
				evidence = "Show canceled"
			}
			w := workflowForTest(t, []workflowTool{
				{name: "search_web", execute: func(context.Context, tool.Scope, json.RawMessage) (tool.Result, error) {
					return tool.Result{Content: evidence}, nil
				}},
				{name: "propose_task", mutating: true, execute: func(_ context.Context, _ tool.Scope, args json.RawMessage) (tool.Result, error) {
					proposals++
					if string(args) != `{"time":"09:30"}` {
						t.Fatalf("args=%s", args)
					}
					return tool.Result{Content: `{"status":"proposed"}`}, nil
				}},
			}, func(_ context.Context, state ToolState, _ []tool.Spec) ([]string, error) {
				selections++
				if len(state.Results) == 0 {
					if canceled {
						return []string{"search_web", "propose_task"}, nil
					}
					return []string{"search_web"}, nil
				}
				if state.Results[0].Content != evidence {
					t.Fatal("missing search evidence")
				}
				if canceled || len(state.Results) > 1 {
					return nil, nil
				}
				return []string{"propose_task"}, nil
			}, func(_ context.Context, state ToolState, specs []tool.Spec) (map[string]PreparedToolArguments, error) {
				args := map[string]PreparedToolArguments{}
				for _, spec := range specs {
					value := `{}`
					if spec.Name == "propose_task" {
						if canceled || len(state.Results) != 1 {
							t.Fatal("proposal built without valid evidence")
						}
						value = `{"time":"09:30"}`
					}
					args[spec.Name] = PreparedToolArguments{Arguments: json.RawMessage(value)}
				}
				return args, nil
			})
			result, err := w.Run(context.Background(), tool.Scope{}, ResponseContext{Query: "conditional or search-dependent reminder"})
			want := 1
			if canceled {
				want = 0
			}
			if err != nil || proposals != want || selections < 2 || result.ProposalCreated != !canceled {
				t.Fatalf("result=%+v proposals=%d selections=%d err=%v", result, proposals, selections, err)
			}
		})
	}
}

func TestToolWorkflowHardRoundLimit(t *testing.T) {
	var registered []workflowTool
	for i := 0; i < MaxToolSelectionRounds+2; i++ {
		registered = append(registered, workflowTool{name: fmt.Sprintf("read_%d", i), execute: func(context.Context, tool.Scope, json.RawMessage) (tool.Result, error) {
			return tool.Result{Content: "done"}, nil
		}})
	}
	selections := 0
	w := workflowForTest(t, registered, func(_ context.Context, _ ToolState, specs []tool.Spec) ([]string, error) {
		selections++
		return []string{specs[0].Name}, nil
	}, func(_ context.Context, _ ToolState, specs []tool.Spec) (map[string]PreparedToolArguments, error) {
		return map[string]PreparedToolArguments{specs[0].Name: {Arguments: json.RawMessage(`{}`)}}, nil
	})
	result, err := w.Run(context.Background(), tool.Scope{}, ResponseContext{})
	if err != nil || selections != MaxToolSelectionRounds || len(result.Results) != MaxToolSelectionRounds+1 || result.Results[len(result.Results)-1].Name != "workflow" {
		t.Fatalf("result=%+v selections=%d err=%v", result, selections, err)
	}
}
