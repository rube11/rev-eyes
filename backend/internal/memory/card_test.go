package memory

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCardNormalize(t *testing.T) {
	card := Card{
		Topics:  []Topic{" Work ", TopicFriends, TopicWork, ""},
		Kind:    " Relationship ",
		Title:   "  Sam is a work friend  ",
		Summary: "  Sam works with the user and is also a friend.  ",
		Details: []Detail{
			{Key: " Role ", Value: " coworker "},
			{Key: "role", Value: "Coworker"},
			{},
		},
		Entities: []Entity{
			{Type: " Person ", Name: " Sam "},
			{Type: EntityPerson, Name: "sam"},
			{},
		},
	}.Normalize()

	want := Card{
		Topics:  []Topic{TopicWork, TopicFriends},
		Kind:    KindRelationship,
		Title:   "Sam is a work friend",
		Summary: "Sam works with the user and is also a friend.",
		Details: []Detail{{Key: "role", Value: "coworker"}},
		Entities: []Entity{
			{Type: EntityPerson, Name: "Sam"},
		},
	}
	if !reflect.DeepEqual(card, want) {
		t.Fatalf("Normalize() = %#v, want %#v", card, want)
	}
}

func TestCardValidate(t *testing.T) {
	valid := Card{
		Topics:  []Topic{TopicPersonal},
		Kind:    KindFact,
		Title:   "User lives in Portland",
		Summary: "The user lives in Portland.",
		Entities: []Entity{
			{Type: EntityPlace, Name: "Portland"},
		},
	}

	tests := map[string]Card{
		"missing topics": {Kind: valid.Kind, Title: valid.Title, Summary: valid.Summary},
		"too many topics": {
			Topics: []Topic{TopicPersonal, TopicPlaces, TopicWork, TopicOther},
			Kind:   valid.Kind, Title: valid.Title, Summary: valid.Summary,
		},
		"invalid topic":   {Topics: []Topic{"unknown"}, Kind: valid.Kind, Title: valid.Title, Summary: valid.Summary},
		"invalid kind":    {Topics: valid.Topics, Kind: "unknown", Title: valid.Title, Summary: valid.Summary},
		"missing title":   {Topics: valid.Topics, Kind: valid.Kind, Summary: valid.Summary},
		"missing summary": {Topics: valid.Topics, Kind: valid.Kind, Title: valid.Title},
		"long title": {
			Topics: valid.Topics, Kind: valid.Kind,
			Title: strings.Repeat("a", maxTitleLength+1), Summary: valid.Summary,
		},
		"long summary": {
			Topics: valid.Topics, Kind: valid.Kind,
			Title: valid.Title, Summary: strings.Repeat("a", maxSummaryLength+1),
		},
		"invalid entity": {
			Topics: valid.Topics, Kind: valid.Kind, Title: valid.Title, Summary: valid.Summary,
			Entities: []Entity{{Type: "unknown", Name: "Portland"}},
		},
	}

	if err := valid.Validate(); err != nil {
		t.Fatalf("valid card error = %v", err)
	}
	for name, card := range tests {
		t.Run(name, func(t *testing.T) {
			if err := card.Validate(); !errors.Is(err, ErrCardInvalid) {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}
