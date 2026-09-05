package websearch

import (
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Test-only offline candidates, not production selection policy. BM25's k1/b and
// positive IDF follow Lucene's documented defaults/formula:
// https://lucene.apache.org/core/9_12_2/core/org/apache/lucene/search/similarities/BM25Similarity.html
// The corpus is all eligible complete owned blocks from this one page. No
// cross-page index, training, provider, synonym expansion or field boosts.
const lexicalBM25K1 = 1.2
const lexicalBM25B = 0.75
const lexicalMMRLambda = 0.7 // One predeclared comparison, not a fitted parameter.

func rankBM25Chunks(query string, candidates []textChunk) []textChunk {
	querySet := lexicalTokenSet(query)
	if len(querySet) == 0 || len(candidates) == 0 {
		return nil
	}
	// Sort query terms so floating-point summation and ties are deterministic.
	terms := make([]string, 0, len(querySet))
	for term := range querySet {
		terms = append(terms, term)
	}
	sort.Strings(terms)
	frequencies := make([]map[string]int, len(candidates))
	lengths := make([]int, len(candidates))
	documentFrequency := make(map[string]int)
	totalLength := 0
	for i, candidate := range candidates {
		tokens := lexicalTokens(candidate.text)
		frequencies[i] = make(map[string]int)
		for _, token := range tokens {
			frequencies[i][token]++
		}
		for term := range frequencies[i] {
			documentFrequency[term]++
		}
		lengths[i] = len(tokens)
		totalLength += len(tokens)
	}
	if totalLength == 0 {
		return nil
	}
	count := float64(len(candidates))
	averageLength := float64(totalLength) / count
	var ranked []textChunk
	for i, candidate := range candidates {
		candidate.score = 0
		for _, term := range terms {
			frequency := float64(frequencies[i][term])
			if frequency == 0 {
				continue
			}
			df := float64(documentFrequency[term])
			idf := math.Log1p((count - df + 0.5) / (df + 0.5))
			normalization := lexicalBM25K1 * (1 - lexicalBM25B + lexicalBM25B*float64(lengths[i])/averageLength)
			candidate.score += idf * frequency * (lexicalBM25K1 + 1) / (frequency + normalization)
		}
		if candidate.score > 0 {
			ranked = append(ranked, candidate)
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].order < ranked[j].order
		}
		return ranked[i].score > ranked[j].score
	})
	return ranked
}

// Greedy MMR chooses only fitting complete blocks, using BM25/max(BM25) as
// relevance and maximum exact-token Jaccard overlap with selected blocks as
// redundancy. It does not infer facets, dates, diet, branch, or entailment.
// Selecting at most three blocks bounds this pass to three corpus scans.
func diversifyBM25Chunks(ranked []textChunk) []textChunk {
	if len(ranked) == 0 {
		return nil
	}
	maxScore := ranked[0].score
	if maxScore <= 0 {
		return nil
	}
	sets := make([]map[string]struct{}, len(ranked))
	for i, candidate := range ranked {
		sets[i] = lexicalTokenSet(candidate.text)
	}
	var selected []textChunk
	var selectedIndices []int
	used := 0
	for len(selected) < maxChunksPerSource {
		best, bestScore := -1, math.Inf(-1)
		for i, candidate := range ranked {
			alreadySelected := false
			for _, previous := range selectedIndices {
				if i == previous {
					alreadySelected = true
				}
			}
			size := utf8.RuneCountInString(candidate.text)
			if len(selected) > 0 {
				size += 2
			}
			if alreadySelected || used+size > maxSnippetLength {
				continue
			}
			redundancy := 0.0
			for _, previous := range selectedIndices {
				redundancy = math.Max(redundancy, lexicalJaccard(sets[i], sets[previous]))
			}
			score := lexicalMMRLambda*candidate.score/maxScore - (1-lexicalMMRLambda)*redundancy
			if score > bestScore || (score == bestScore && (best == -1 || candidate.order < ranked[best].order)) {
				best, bestScore = i, score
			}
		}
		if best == -1 {
			break
		}
		if len(selected) > 0 {
			used += 2
		}
		used += utf8.RuneCountInString(ranked[best].text)
		selected = append(selected, ranked[best])
		selectedIndices = append(selectedIndices, best)
	}
	return selected
}

func lexicalJaccard(left, right map[string]struct{}) float64 {
	if len(left) > len(right) {
		left, right = right, left
	}
	intersection := 0
	for token := range left {
		if _, found := right[token]; found {
			intersection++
		}
	}
	union := len(left) + len(right) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func lexicalTokenSet(text string) map[string]struct{} {
	tokens := make(map[string]struct{})
	for _, token := range lexicalTokens(text) {
		tokens[token] = struct{}{}
	}
	return tokens
}

// Exact alphanumeric tokens share the existing stop-word list. Preserve term
// frequency, split identifiers at lowercase-to-uppercase/acronym boundaries,
// and lowercase afterward: Client.Timeout, ClientTimeout and client timeout
// match without substring matches such as time inside timeout or vegas in ve.
func lexicalTokens(text string) []string {
	var tokens []string
	appendToken := func(runes []rune) {
		if len(runes) < 2 {
			return
		}
		token := strings.ToLower(string(runes))
		if _, ignored := queryStopWords[token]; !ignored {
			tokens = append(tokens, token)
		}
	}
	for _, word := range strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		runes := []rune(word)
		start := 0
		for i := 1; i < len(runes); i++ {
			if unicode.IsUpper(runes[i]) && (unicode.IsLower(runes[i-1]) ||
				(unicode.IsUpper(runes[i-1]) && i+1 < len(runes) && unicode.IsLower(runes[i+1]))) {
				appendToken(runes[start:i])
				start = i
			}
		}
		appendToken(runes[start:])
	}
	return tokens
}
