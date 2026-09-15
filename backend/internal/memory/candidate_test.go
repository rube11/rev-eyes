package memory

import (
	"reflect"
	"testing"
)

func TestCandidateNormalizeCanonicalizesCollectionsAndKey(t *testing.T) {
	candidate := Candidate{
		Card: Card{
			Topics:  []Topic{TopicPreferences, TopicHealth},
			Kind:    KindPreference,
			Title:   "Food preferences",
			Summary: "The user likes steak and chicken.",
			Details: []Detail{
				{Key: "food", Value: "steak"},
				{Key: "food", Value: "chicken"},
			},
			Entities: []Entity{
				{Type: EntityOther, Name: "Steak"},
				{Type: EntityOther, Name: "Chicken"},
			},
		},
		MemoryKey: " Profile.Food.Preferences ",
		Retention: RetentionDurable,
	}.Normalize()

	if candidate.MemoryKey != "profile.food.preferences" {
		t.Fatalf("memory key = %q", candidate.MemoryKey)
	}
	if !reflect.DeepEqual(candidate.Card.Topics, []Topic{TopicHealth, TopicPreferences}) {
		t.Fatalf("topics = %#v", candidate.Card.Topics)
	}
	if !reflect.DeepEqual(candidate.Card.Details, []Detail{
		{Key: "food", Value: "chicken"},
		{Key: "food", Value: "steak"},
	}) {
		t.Fatalf("details = %#v", candidate.Card.Details)
	}
	if !reflect.DeepEqual(candidate.Card.Entities, []Entity{
		{Type: EntityOther, Name: "Chicken"},
		{Type: EntityOther, Name: "Steak"},
	}) {
		t.Fatalf("entities = %#v", candidate.Card.Entities)
	}
}
