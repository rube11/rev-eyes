package assistant

import (
	"context"
	"reflect"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/memory"
)

func TestRouterRoutesExplicitRememberWithoutMemoryPayload(t *testing.T) {
	router := NewRouter(func(context.Context, string) (string, error) {
		return `{
			"action":"remember",
			"query":" Maya is the user's boss. ",
			"memory_lookup":{"terms":["boss"],"topics":["work"],"kinds":["relationship"],"entities":["Maya"]}
		}`, nil
	})

	decision, err := router.Route(context.Background(), "Remember that Maya is my boss")
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if decision.Action != ActionRemember || decision.Query != "Maya is the user's boss." {
		t.Fatalf("Route() = %#v", decision)
	}
	if !decision.MemoryLookup.Empty() {
		t.Fatalf("memory lookup = %#v, want empty", decision.MemoryLookup)
	}
}

func TestRouterNormalizesMemoryLookupForResponse(t *testing.T) {
	router := NewRouter(func(context.Context, string) (string, error) {
		return `{
			"action":"respond",
			"query":"What time is it?",
			"memory_lookup":{
				"terms":[" Boss ","boss","manager"],
				"topics":["work","relationships"],
				"kinds":["relationship"],
				"entities":[" Maya ","maya"]
			}
		}`, nil
	})

	decision, err := router.Route(context.Background(), "What time is it?")
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if decision.Action != ActionRespond {
		t.Fatalf("Route() decision = %#v", decision)
	}
	wantLookup := memory.Lookup{
		Terms:    []string{"boss", "manager"},
		Topics:   []memory.Topic{memory.TopicWork, memory.TopicRelationships},
		Kinds:    []memory.Kind{memory.KindRelationship},
		Entities: []string{"maya"},
	}
	if !reflect.DeepEqual(decision.MemoryLookup, wantLookup) {
		t.Fatalf("memory lookup = %#v, want %#v", decision.MemoryLookup, wantLookup)
	}
}

func TestRouterKeepsMemoryLookupForTaskProposal(t *testing.T) {
	router := NewRouter(func(context.Context, string) (string, error) {
		return `{
			"action":"propose_task",
			"query":"I should call Maya tomorrow.",
			"memory_lookup":{
				"terms":[" Maya ","maya"],
				"topics":["work"],
				"kinds":[],
				"entities":[" Maya "]
			}
		}`, nil
	})

	decision, err := router.Route(context.Background(), "I should call Maya tomorrow")
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	want := memory.Lookup{
		Terms:    []string{"maya"},
		Topics:   []memory.Topic{memory.TopicWork},
		Kinds:    []memory.Kind{},
		Entities: []string{"maya"},
	}
	if decision.Action != ActionProposeTask ||
		!reflect.DeepEqual(decision.MemoryLookup, want) {
		t.Fatalf("Route() = %#v, want lookup %#v", decision, want)
	}
}

func TestRouterKeepsMemoryLookupForWatchProposal(t *testing.T) {
	router := NewRouter(func(context.Context, string) (string, error) {
		return `{
			"action":"propose_watch",
			"query":"Tell me if Nintendo announces its next console.",
			"memory_lookup":{"terms":["nintendo"],"topics":["preferences"],"kinds":[],"entities":["Nintendo"]}
		}`, nil
	})

	decision, err := router.Route(context.Background(), "Tell me if Nintendo announces its next console")
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if decision.Action != ActionProposeWatch ||
		!reflect.DeepEqual(decision.MemoryLookup.Terms, []string{"nintendo"}) {
		t.Fatalf("Route() = %#v", decision)
	}
}
