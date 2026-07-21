package memory

import "strings"

// Lookup describes an exact, unranked memory search.
type Lookup struct {
	Terms    []string `json:"terms"`
	Topics   []Topic  `json:"topics"`
	Kinds    []Kind   `json:"kinds"`
	Entities []string `json:"entities"`
}

func (l Lookup) Normalize() Lookup {
	l.Terms = normalizeStrings(l.Terms)
	l.Entities = normalizeStrings(l.Entities)
	return l
}

// Empty reports whether the lookup has anything specific to match.
func (l Lookup) Empty() bool {
	return len(l.Terms) == 0 && len(l.Entities) == 0
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
