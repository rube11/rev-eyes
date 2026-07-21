package memory

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxTopics        = 3
	maxTitleLength   = 120
	maxSummaryLength = 500
)

type Topic string

const (
	TopicWork          Topic = "work"
	TopicPersonal      Topic = "personal"
	TopicFriends       Topic = "friends"
	TopicFamily        Topic = "family"
	TopicRelationships Topic = "relationships"
	TopicHealth        Topic = "health"
	TopicPreferences   Topic = "preferences"
	TopicGoals         Topic = "goals"
	TopicPlaces        Topic = "places"
	TopicOther         Topic = "other"
)

type Kind string

const (
	KindFact         Kind = "fact"
	KindPreference   Kind = "preference"
	KindRelationship Kind = "relationship"
	KindEvent        Kind = "event"
	KindGoal         Kind = "goal"
	KindInstruction  Kind = "instruction"
)

type EntityType string

const (
	EntityPerson       EntityType = "person"
	EntityPlace        EntityType = "place"
	EntityOrganization EntityType = "organization"
	EntityProject      EntityType = "project"
	EntityEvent        EntityType = "event"
	EntityOther        EntityType = "other"
)

var ErrCardInvalid = errors.New("memory card is invalid")

// Detail captures one structured attribute of a memory.
type Detail struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Entity identifies a person, place, organization, project, or event.
type Entity struct {
	Type EntityType `json:"type"`
	Name string     `json:"name"`
}

// Card is one durable, categorized memory extracted from an utterance.
type Card struct {
	Topics   []Topic  `json:"topics"`
	Kind     Kind     `json:"kind"`
	Title    string   `json:"title"`
	Summary  string   `json:"summary"`
	Details  []Detail `json:"details"`
	Entities []Entity `json:"entities"`
}

func (c Card) Normalize() Card {
	c.Kind = Kind(strings.ToLower(strings.TrimSpace(string(c.Kind))))
	c.Title = strings.TrimSpace(c.Title)
	c.Summary = strings.TrimSpace(c.Summary)

	topics := make([]Topic, 0, len(c.Topics))
	seenTopics := make(map[Topic]struct{}, len(c.Topics))
	for _, topic := range c.Topics {
		topic = Topic(strings.ToLower(strings.TrimSpace(string(topic))))
		if _, found := seenTopics[topic]; topic == "" || found {
			continue
		}
		seenTopics[topic] = struct{}{}
		topics = append(topics, topic)
	}
	c.Topics = topics

	details := make([]Detail, 0, len(c.Details))
	seenDetails := make(map[string]struct{}, len(c.Details))
	for _, detail := range c.Details {
		detail.Key = normalizeKey(detail.Key)
		detail.Value = strings.TrimSpace(detail.Value)
		key := detail.Key + "\x00" + strings.ToLower(detail.Value)
		if _, found := seenDetails[key]; detail.Key == "" || detail.Value == "" || found {
			continue
		}
		seenDetails[key] = struct{}{}
		details = append(details, detail)
	}
	c.Details = details

	entities := make([]Entity, 0, len(c.Entities))
	seenEntities := make(map[string]struct{}, len(c.Entities))
	for _, entity := range c.Entities {
		entity.Type = EntityType(strings.ToLower(strings.TrimSpace(string(entity.Type))))
		entity.Name = strings.TrimSpace(entity.Name)
		key := string(entity.Type) + "\x00" + strings.ToLower(entity.Name)
		if _, found := seenEntities[key]; entity.Type == "" || entity.Name == "" || found {
			continue
		}
		seenEntities[key] = struct{}{}
		entities = append(entities, entity)
	}
	c.Entities = entities

	return c
}

func normalizeKey(value string) string {
	parts := strings.FieldsFunc(strings.ToLower(value), func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsDigit(character)
	})
	return strings.Join(parts, "_")
}

func (c Card) Validate() error {
	if len(c.Topics) == 0 || len(c.Topics) > maxTopics {
		return fmt.Errorf("%w: one to three topics are required", ErrCardInvalid)
	}
	for _, topic := range c.Topics {
		if !validTopic(topic) {
			return fmt.Errorf("%w: invalid topic %q", ErrCardInvalid, topic)
		}
	}
	if !validKind(c.Kind) {
		return fmt.Errorf("%w: invalid kind %q", ErrCardInvalid, c.Kind)
	}
	if c.Title == "" {
		return fmt.Errorf("%w: title is required", ErrCardInvalid)
	}
	if utf8.RuneCountInString(c.Title) > maxTitleLength {
		return fmt.Errorf("%w: title is too long", ErrCardInvalid)
	}
	if c.Summary == "" {
		return fmt.Errorf("%w: summary is required", ErrCardInvalid)
	}
	if utf8.RuneCountInString(c.Summary) > maxSummaryLength {
		return fmt.Errorf("%w: summary is too long", ErrCardInvalid)
	}
	for _, detail := range c.Details {
		if detail.Key == "" || detail.Value == "" {
			return fmt.Errorf("%w: detail key and value are required", ErrCardInvalid)
		}
	}
	for _, entity := range c.Entities {
		if !validEntityType(entity.Type) || entity.Name == "" {
			return fmt.Errorf("%w: invalid entity", ErrCardInvalid)
		}
	}
	return nil
}

func TopicValues() []string {
	return []string{
		string(TopicWork),
		string(TopicPersonal),
		string(TopicFriends),
		string(TopicFamily),
		string(TopicRelationships),
		string(TopicHealth),
		string(TopicPreferences),
		string(TopicGoals),
		string(TopicPlaces),
		string(TopicOther),
	}
}

func KindValues() []string {
	return []string{
		string(KindFact),
		string(KindPreference),
		string(KindRelationship),
		string(KindEvent),
		string(KindGoal),
		string(KindInstruction),
	}
}

func EntityTypeValues() []string {
	return []string{
		string(EntityPerson),
		string(EntityPlace),
		string(EntityOrganization),
		string(EntityProject),
		string(EntityEvent),
		string(EntityOther),
	}
}

func validTopic(topic Topic) bool {
	for _, candidate := range TopicValues() {
		if string(topic) == candidate {
			return true
		}
	}
	return false
}

func validKind(kind Kind) bool {
	for _, candidate := range KindValues() {
		if string(kind) == candidate {
			return true
		}
	}
	return false
}

func validEntityType(entityType EntityType) bool {
	for _, candidate := range EntityTypeValues() {
		if string(entityType) == candidate {
			return true
		}
	}
	return false
}
