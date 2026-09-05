package websearch

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestLexicalRankingTokensPreserveFrequencyAndSplitIdentifiers(t *testing.T) {
	want := []string{"client", "timeout", "client", "timeout", "http", "server", "response", "writer", "alpha", "alpha", "http2"}
	if got := lexicalTokens("Client.Timeout ClientTimeout HTTPServer ResponseWriter alpha ALPHA and HTTP2"); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected exact tokens: %#v", got)
	}
	candidates := []textChunk{{text: "A reopened workshop serves overtime meals.", order: 0}, {text: "Open workshop time slots are listed here.", order: 1}}
	if got := rankBM25Chunks("open time", candidates); len(got) != 1 || got[0].order != 1 {
		t.Fatalf("substring evidence matched exact query tokens: %#v", got)
	}
}

func TestLexicalRankingBM25FormulaAndStableInput(t *testing.T) {
	candidates := []textChunk{{text: "alpha beta", order: 0}, {text: "alpha alpha alpha", order: 1}, {text: "gamma", order: 2}}
	before := append([]textChunk(nil), candidates...)
	got := rankBM25Chunks("alpha alpha", candidates)
	if len(got) != 2 {
		t.Fatalf("wrong matching corpus size: %d", len(got))
	}
	idf := math.Log1p(1.5 / 2.5)
	want := map[int]float64{0: idf, 1: idf * 3 * 2.2 / (3 + 1.2*(0.25+0.75*3/2))}
	for _, candidate := range got {
		if math.Abs(candidate.score-want[candidate.order]) > 1e-12 {
			t.Fatalf("BM25 formula drift: %+v, want %g", candidate, want[candidate.order])
		}
	}
	if !reflect.DeepEqual(before, candidates) {
		t.Fatal("ranker mutated the candidate pool")
	}
	if !reflect.DeepEqual(got, rankBM25Chunks("alpha", candidates)) {
		t.Fatal("repeated query words unexpectedly boosted scores")
	}
	for i := 0; i < 20; i++ {
		if !reflect.DeepEqual(got, rankBM25Chunks("alpha alpha", candidates)) {
			t.Fatal("ranking is nondeterministic")
		}
	}
	if len(rankBM25Chunks("and the", candidates)) != 0 || len(rankBM25Chunks("absent", candidates)) != 0 || len(rankBM25Chunks("alpha", nil)) != 0 {
		t.Fatal("empty query/corpus or absent terms produced evidence")
	}
}

func TestLexicalRankingMMRPredeclaredDuplicateControl(t *testing.T) {
	ranked := []textChunk{
		{text: "Alpha grounds public admission rules.", score: 10, order: 0},
		{text: "Alpha grounds public admission rules.", score: 10, order: 1},
		{text: "Evening entry requires advance registration.", score: 9, order: 2},
		{text: "Children need an accompanying adult.", score: 8, order: 3},
	}
	before := append([]textChunk(nil), ranked...)
	selected := diversifyBM25Chunks(ranked)
	if len(selected) != 3 || selected[0].order != 0 || selected[1].order != 2 || selected[2].order != 3 {
		t.Fatalf("fixed MMR arm did not penalize exact duplicate: %+v", selected)
	}
	if !reflect.DeepEqual(before, ranked) {
		t.Fatal("diversification mutated BM25 scores/order")
	}
	if lexicalJaccard(lexicalTokenSet("alpha beta"), lexicalTokenSet("beta gamma")) != 1.0/3 || lexicalJaccard(nil, nil) != 0 {
		t.Fatal("Jaccard formula drift")
	}
}

func TestLexicalRankingWholeUnitBudgetAndOwnership(t *testing.T) {
	long := "Section: Alpha rules\n" + strings.Repeat("detail ", 110) + "No public access after sunset."
	short := "Section: Beta rules\nMembers only; not open to the public."
	ranked := []textChunk{{text: long, score: 10, order: 3}, {text: long, score: 9, order: 2}, {text: short, score: 8, order: 0}}
	for _, got := range []string{selectRankedChunks(ranked), selectRankedChunks(diversifyBM25Chunks(ranked))} {
		if !strings.HasPrefix(got, short) || !strings.Contains(got, long) {
			t.Fatalf("ownership or document order changed: %q", got)
		}
		for _, block := range completePageExcerpts(got, maxSnippetLength) {
			if block != long && block != short {
				t.Fatal("ranker emitted partial or merged evidence")
			}
		}
	}
}
