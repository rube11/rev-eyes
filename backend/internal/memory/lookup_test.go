package memory

import (
	"reflect"
	"testing"
)

func TestLookupNormalize(t *testing.T) {
	lookup := Lookup{
		Query:    "  What should I make   for dinner? ",
		Terms:    []string{" Boss ", "boss", "Manager"},
		Topics:   []Topic{TopicWork, TopicRelationships},
		Kinds:    []Kind{KindRelationship},
		Entities: []string{" Maya ", "maya"},
	}.Normalize()

	want := Lookup{
		Query:    "What should I make for dinner?",
		Terms:    []string{"boss", "manager"},
		Topics:   []Topic{TopicWork, TopicRelationships},
		Kinds:    []Kind{KindRelationship},
		Entities: []string{"maya"},
	}
	if !reflect.DeepEqual(lookup, want) {
		t.Fatalf("Normalize() = %#v, want %#v", lookup, want)
	}
	if lookup.Empty() {
		t.Fatal("Empty() = true")
	}
}

func TestLookupWithOnlyQueryIsNotEmpty(t *testing.T) {
	lookup := Lookup{Query: "  dinner ideas  "}.Normalize()
	if lookup.Query != "dinner ideas" {
		t.Fatalf("Normalize().Query = %q", lookup.Query)
	}
	if lookup.Empty() {
		t.Fatal("Empty() = true")
	}
}

func TestLookupWithOnlyFiltersIsEmpty(t *testing.T) {
	lookup := Lookup{
		Topics: []Topic{TopicWork},
		Kinds:  []Kind{KindFact},
	}
	if !lookup.Empty() {
		t.Fatal("Empty() = false")
	}
}

func TestLookupNormalizeBoundsStructuredHints(t *testing.T) {
	lookup := Lookup{
		Terms: []string{
			"one", "two", "three", "four", "five", "six",
		},
		Topics: []Topic{
			TopicHealth,
			TopicGoals,
			TopicHealth,
			TopicPreferences,
			TopicWork,
			Topic("invalid"),
		},
		Kinds: []Kind{
			KindGoal,
			KindPreference,
			KindGoal,
			Kind("invalid"),
		},
	}.Normalize()

	if !reflect.DeepEqual(lookup.Terms, []string{"one", "two", "three", "four", "five"}) {
		t.Fatalf("terms = %#v", lookup.Terms)
	}
	if !reflect.DeepEqual(lookup.Topics, []Topic{TopicHealth, TopicGoals, TopicPreferences}) {
		t.Fatalf("topics = %#v", lookup.Topics)
	}
	if !reflect.DeepEqual(lookup.Kinds, []Kind{KindGoal, KindPreference}) {
		t.Fatalf("kinds = %#v", lookup.Kinds)
	}
}
