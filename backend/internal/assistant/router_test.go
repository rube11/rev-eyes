package assistant

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/memory"
)

func TestRouterReturnsNormalizedMemoryCard(t *testing.T) {
	router := NewRouter(func(context.Context, string) (string, error) {
		return `{
			"action":"remember",
			"query":" Maya is the user's boss. ",
			"memory_lookup":{"terms":["boss"],"topics":["work"],"kinds":["relationship"],"entities":["Maya"]},
			"memory":{
				"topics":["work","relationships"],
				"kind":"relationship",
				"title":" Maya is my boss ",
				"summary":" Maya is the user's boss. ",
				"details":[{"key":" relationship ","value":" boss "}],
				"entities":[{"type":"person","name":" Maya "}]
			}
		}`, nil
	})

	decision, err := router.Route(context.Background(), "Remember that Maya is my boss")
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	want := &memory.Card{
		Topics:  []memory.Topic{memory.TopicWork, memory.TopicRelationships},
		Kind:    memory.KindRelationship,
		Title:   "Maya is my boss",
		Summary: "Maya is the user's boss.",
		Details: []memory.Detail{{Key: "relationship", Value: "boss"}},
		Entities: []memory.Entity{
			{Type: memory.EntityPerson, Name: "Maya"},
		},
	}
	if decision.Action != ActionRemember || !reflect.DeepEqual(decision.Memory, want) {
		t.Fatalf("Route() = %#v, want memory %#v", decision, want)
	}
	if !decision.MemoryLookup.Empty() {
		t.Fatalf("memory lookup = %#v, want empty", decision.MemoryLookup)
	}
}

func TestRouterRejectsRememberWithoutMemoryCard(t *testing.T) {
	router := NewRouter(func(context.Context, string) (string, error) {
		return `{"action":"remember","query":"something","memory_lookup":{"terms":[],"topics":[],"kinds":[],"entities":[]},"memory":null}`, nil
	})

	decision, err := router.Route(context.Background(), "remember something")
	if !errors.Is(err, memory.ErrCardInvalid) {
		t.Fatalf("Route() error = %v", err)
	}
	if decision.Action != ActionIgnore {
		t.Fatalf("Route() decision = %#v", decision)
	}
}

func TestRouterDropsMemoryForOtherActions(t *testing.T) {
	router := NewRouter(func(context.Context, string) (string, error) {
		return `{
			"action":"respond",
			"query":"What time is it?",
			"memory_lookup":{
				"terms":[" Boss ","boss","manager"],
				"topics":["work","relationships"],
				"kinds":["relationship"],
				"entities":[" Maya ","maya"]
			},
			"memory":{
				"topics":["other"],
				"kind":"fact",
				"title":"Ignore me",
				"summary":"Ignore me.",
				"details":[],
				"entities":[]
			}
		}`, nil
	})

	decision, err := router.Route(context.Background(), "What time is it?")
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if decision.Action != ActionRespond || decision.Memory != nil {
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
			},
			"memory":null
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
