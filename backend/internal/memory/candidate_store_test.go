package memory

import "testing"

func TestWebSearchQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
		terms []string
		want  string
	}{
		{
			name:  "query and phrases",
			query: "what should i make for dinner",
			terms: []string{"protein target", "chicken rice"},
			want:  `what should i make for dinner OR "protein target" OR "chicken rice"`,
		},
		{
			name:  "terms without query",
			terms: []string{"student", "affordable meals"},
			want:  `"student" OR "affordable meals"`,
		},
		{
			name:  "trims and ignores blanks",
			query: `  "pasta"  `,
			terms: []string{"  mushrooms  ", "", "   "},
			want:  `pasta OR "mushrooms"`,
		},
		{
			name: "empty",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := webSearchQuery(test.query, test.terms); got != test.want {
				t.Fatalf("webSearchQuery() = %q, want %q", got, test.want)
			}
		})
	}
}
