package websearch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

type lexicalReplayQuery struct {
	Query      string                   `json:"query"`
	Origins    []string                 `json:"origins"`
	Arms       []lexicalReplayArm       `json:"arms"`
	Candidates []lexicalReplayCandidate `json:"complete_candidates"`
}

type lexicalReplayCandidate struct {
	Block         string  `json:"block"`
	Order         int     `json:"document_order"`
	Runes         int     `json:"runes"`
	BaselineRank  int     `json:"baseline_rank,omitempty"`
	BaselineScore float64 `json:"baseline_score"`
	BM25Rank      int     `json:"bm25_rank,omitempty"`
	BM25Score     float64 `json:"bm25_score"`
}

type lexicalReplayArm struct {
	Name   string   `json:"name"`
	Blocks []string `json:"selected_blocks"`
	Runes  int      `json:"selected_runes"`
}

type lexicalReplayPage struct {
	CaseID    string               `json:"case_id"`
	URL       string               `json:"url"`
	MarkupSHA string               `json:"markup_sha256"`
	TextSHA   string               `json:"normalized_text_sha256"`
	Queries   []lexicalReplayQuery `json:"queries"`
}

// This opt-in evaluation only reads immutable saved HTML and literal query
// strings. It neither fetches pages nor calls a search/model provider. Existing
// outputs cannot be overwritten, and changing a frozen input fails closed.
func TestOfflineLexicalRankingReplay(t *testing.T) {
	outputPath := os.Getenv("OFFLINE_LEXICAL_RANKING_REPORT")
	if outputPath == "" {
		t.Skip("set OFFLINE_LEXICAL_RANKING_REPORT to a new offline output path")
	}
	readFrozen := func(name, wantSHA string, target any) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", name))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		actual := hex.EncodeToString(digest[:])
		if actual != wantSHA {
			t.Fatalf("frozen input %s hash mismatch", name)
		}
		if err := json.Unmarshal(data, target); err != nil {
			t.Fatal(err)
		}
		return actual
	}
	var input struct {
		Pages []struct {
			ownedParagraphFixture
			AcquisitionError string `json:"acquisition_error"`
		} `json:"pages"`
	}
	inputSHA := readFrozen("extractor-ablation-input2-2026-09-04.json", "cb12364003a215dc61ef1139c2b7c57329fdf0c67bd1325d5841e812e3d11e03", &input)
	var trace struct {
		Runs []struct {
			Case struct {
				ID string `json:"id"`
			} `json:"case"`
			Searches []struct {
				Arguments struct {
					Query string `json:"query"`
				} `json:"arguments"`
			} `json:"searches"`
		} `json:"runs"`
	}
	traceSHA := readFrozen("heldout-pilot-live10-excerpt-refs-2026-09-04.json", "695112fdfa056e8e127fa46263c5f47564ccfeac95e4998b2e95ab4b4aed7cdb", &trace)
	var frozen struct {
		Pages []lexicalReplayPage `json:"pages"`
	}
	baselineSHA := readFrozen("lexical-ranking-baseline-2026-09-04.json", "cd9ad5890f612f72b49028e018fad3134719be4bd73c459b43933d35470ce692", &frozen)
	var pages []lexicalReplayPage
	var skipped []string
	for _, page := range input.Pages {
		if page.AcquisitionError != "" {
			skipped = append(skipped, page.URL+": "+page.AcquisitionError)
			continue
		}
		if len(page.Arms) == 0 || page.Arms[0].Name != "go_current" {
			t.Fatal("missing saved original HTML")
		}
		markup := page.Arms[0].HTML
		digest := sha256.Sum256([]byte(markup))
		if hex.EncodeToString(digest[:]) != page.MarkupSHA {
			t.Fatal("original HTML hash mismatch")
		}
		text := extractReadableText(markup)
		// Match fetchPage: original JSON-LD is a separate evidence channel and
		// retains its own existing admission/size/date restrictions.
		if structured := extractStructuredEvidence(markup); structured != "" {
			text = strings.TrimSpace(text + "\n" + structured)
		}
		digest = sha256.Sum256([]byte(text))
		result := lexicalReplayPage{CaseID: page.CaseID, URL: page.URL, MarkupSHA: page.MarkupSHA, TextSHA: hex.EncodeToString(digest[:])}
		addQuery := func(query, origin string) {
			if strings.TrimSpace(query) == "" {
				return
			}
			for i := range result.Queries {
				if result.Queries[i].Query == query {
					result.Queries[i].Origins = append(result.Queries[i].Origins, origin)
					return
				}
			}
			result.Queries = append(result.Queries, lexicalReplayQuery{Query: query, Origins: []string{origin}})
		}
		addQuery(page.Query, "input2.original_question")
		addQuery(page.CapturedQuery, "input2.last_captured_search_query")
		for _, run := range trace.Runs {
			if run.Case.ID != page.CaseID {
				continue
			}
			for _, search := range run.Searches {
				addQuery(search.Arguments.Query, "live10.literal_search_query")
			}
		}
		for i := range result.Queries {
			query := &result.Queries[i]
			candidates := ownedTextChunks(query.Query, text)
			baseline, bm25 := rankLegacyChunks(query.Query, candidates), rankBM25Chunks(query.Query, candidates)
			query.Arms = []lexicalReplayArm{
				lexicalReplaySelection("baseline", selectRankedChunks(baseline)),
				lexicalReplaySelection("bm25", selectRankedChunks(bm25)),
				lexicalReplaySelection("bm25_mmr", selectRankedChunks(diversifyBM25Chunks(bm25))),
			}
			for _, candidate := range candidates {
				record := lexicalReplayCandidate{Block: candidate.text, Order: candidate.order, Runes: utf8.RuneCountInString(candidate.text)}
				for rank, ranked := range baseline {
					if ranked.order == candidate.order {
						record.BaselineRank, record.BaselineScore = rank+1, ranked.score
						break
					}
				}
				for rank, ranked := range bm25 {
					if ranked.order == candidate.order {
						record.BM25Rank, record.BM25Score = rank+1, ranked.score
						break
					}
				}
				query.Candidates = append(query.Candidates, record)
			}
			for _, arm := range query.Arms {
				if arm.Runes > maxSnippetLength || len(arm.Blocks) > maxChunksPerSource {
					t.Fatal("ranking exceeded evidence budget")
				}
				for _, block := range arm.Blocks {
					found := false
					for _, candidate := range candidates {
						if block == candidate.text {
							found = true
							break
						}
					}
					if !found {
						t.Fatal("ranker mutated or manufactured evidence")
					}
				}
			}
			if selectQueryChunks(query.Query, text) != strings.Join(query.Arms[0].Blocks, "\n\n") {
				t.Fatal("production ranker changed")
			}
		}
		pageIndex := len(pages)
		if pageIndex >= len(frozen.Pages) || result.URL != frozen.Pages[pageIndex].URL || result.TextSHA != frozen.Pages[pageIndex].TextSHA || len(result.Queries) != len(frozen.Pages[pageIndex].Queries) {
			t.Fatal("frozen baseline page/query identity changed")
		}
		for i, query := range result.Queries {
			prior := frozen.Pages[pageIndex].Queries[i]
			if query.Query != prior.Query || !reflect.DeepEqual(query.Origins, prior.Origins) || !reflect.DeepEqual(query.Arms[0], prior.Arms[0]) {
				t.Fatal("refactoring changed frozen baseline selection")
			}
		}
		pages = append(pages, result)
	}
	if len(pages) != 6 || len(skipped) != 1 {
		t.Fatal("frozen acquisition coverage changed")
	}
	report := struct {
		Mode        string              `json:"mode"`
		Scope       string              `json:"scope"`
		InputSHA    string              `json:"saved_html_input_sha256"`
		TraceSHA    string              `json:"live10_query_trace_sha256"`
		BaselineSHA string              `json:"frozen_baseline_sha256"`
		Algorithms  string              `json:"predeclared_algorithms"`
		Skipped     []string            `json:"acquisition_failures_unchanged"`
		Pages       []lexicalReplayPage `json:"pages"`
	}{"offline_frozen_lexical_ranking", "Original HTML for all six successfully acquired input2 pages, with production normalization plus original structured evidence. Queries are exact original/input2-captured/live10-captured strings, deduplicated with origins. Applying every query for a case to its saved page is a controlled sensitivity test, not proof that live10 fetched that page for every query. Whole owned units, three blocks and 1600 runes remain bounded. No model, search-provider or page-fetch calls; fetched-evidence coverage is not a final-answer grade. Complete candidates are all eligible owned units without relevance filtering; omitted rank means no positive lexical match, not unavailable evidence.", inputSHA, traceSHA, baselineSHA, "baseline unchanged; bm25 k1=1.2 b=0.75 positive Lucene IDF, per-page complete-block corpus, exact case-folded tokens with camel/acronym splitting and existing stop words, unique query terms; bm25_mmr lambda=0.7 normalized BM25 relevance minus maximum exact-token Jaccard redundancy, greedily fitting whole units. No parameter sweep, synonyms, venue/domain/facet rules or production promotion.", skipped, pages}
	file, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	writeErr := encoder.Encode(report)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal("failed to write exclusive-create ranking report")
	}
	t.Logf("replayed %d saved pages, %d acquisition failure unchanged", len(pages), len(skipped))
}

func lexicalReplaySelection(name, selected string) lexicalReplayArm {
	return lexicalReplayArm{Name: name, Blocks: completePageExcerpts(selected, maxSnippetLength), Runes: utf8.RuneCountInString(selected)}
}
