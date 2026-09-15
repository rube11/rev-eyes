package memory

import "testing"

func TestHasLookupFilters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		lookup Lookup
		want   bool
	}{
		{name: "empty"},
		{name: "query", lookup: Lookup{Query: "Jolene"}, want: true},
		{name: "term", lookup: Lookup{Terms: []string{"protein target"}}, want: true},
		{name: "entity", lookup: Lookup{Entities: []string{"Jolene"}}, want: true},
		{name: "topic", lookup: Lookup{Topics: []Topic{TopicHealth}}, want: true},
		{name: "kind", lookup: Lookup{Kinds: []Kind{KindGoal}}, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := hasLookupFilters(test.lookup.Normalize()); got != test.want {
				t.Fatalf("hasLookupFilters() = %v, want %v", got, test.want)
			}
		})
	}
}
