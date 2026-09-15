package memory

import "strings"

const (
	maxLookupTerms  = 5
	maxLookupTopics = 3
)

// Lookup contains text and structured hints for relevant-memory retrieval.
type Lookup struct {
	Query    string   `json:"-"`
	Terms    []string `json:"terms"`
	Topics   []Topic  `json:"topics"`
	Kinds    []Kind   `json:"kinds"`
	Entities []string `json:"entities"`
}

func (l Lookup) Normalize() Lookup {
	l.Query = strings.Join(strings.Fields(l.Query), " ")
	l.Terms = normalizeStrings(l.Terms)
	if len(l.Terms) > maxLookupTerms {
		l.Terms = l.Terms[:maxLookupTerms]
	}
	l.Topics = normalizeLookupTopics(l.Topics)
	l.Kinds = normalizeLookupKinds(l.Kinds)
	l.Entities = normalizeStrings(l.Entities)
	return l
}

// Empty reports whether the lookup has anything specific to match.
func (l Lookup) Empty() bool {
	return l.Query == "" && len(l.Terms) == 0 && len(l.Entities) == 0
}

func normalizeStrings(values []string) []string {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.Join(strings.Fields(value), " "))
		if _, found := seen[value]; value == "" || found {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized
}

func normalizeLookupTopics(values []Topic) []Topic {
	normalized := make([]Topic, 0, min(len(values), maxLookupTopics))
	seen := make(map[Topic]struct{}, len(values))
	for _, topic := range values {
		topic = Topic(strings.ToLower(strings.TrimSpace(string(topic))))
		if _, found := seen[topic]; found || !validTopic(topic) {
			continue
		}
		seen[topic] = struct{}{}
		normalized = append(normalized, topic)
		if len(normalized) == maxLookupTopics {
			break
		}
	}
	return normalized
}

func normalizeLookupKinds(values []Kind) []Kind {
	normalized := make([]Kind, 0, len(values))
	seen := make(map[Kind]struct{}, len(values))
	for _, kind := range values {
		kind = Kind(strings.ToLower(strings.TrimSpace(string(kind))))
		if _, found := seen[kind]; found || !validKind(kind) {
			continue
		}
		seen[kind] = struct{}{}
		normalized = append(normalized, kind)
	}
	return normalized
}
