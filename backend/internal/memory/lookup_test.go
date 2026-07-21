package memory

import (
	"reflect"
	"testing"
)

func TestLookupNormalize(t *testing.T) {
	lookup := Lookup{
		Terms:    []string{" Boss ", "boss", "Manager"},
		Topics:   []Topic{TopicWork, TopicRelationships},
		Kinds:    []Kind{KindRelationship},
		Entities: []string{" Maya ", "maya"},
	}.Normalize()

	want := Lookup{
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

func TestLookupWithOnlyFiltersIsEmpty(t *testing.T) {
	lookup := Lookup{
		Topics: []Topic{TopicWork},
		Kinds:  []Kind{KindFact},
	}
	if !lookup.Empty() {
		t.Fatal("Empty() = false")
	}
}
