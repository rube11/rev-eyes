package websearch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

type ownedParagraphFixture struct {
	URL           string `json:"url"`
	CaseID        string `json:"case_id"`
	Query         string `json:"query"`
	CapturedQuery string `json:"last_captured_search_query"`
	MarkupSHA     string `json:"markup_sha256"`
	Arms          []struct {
		Name string `json:"name"`
		HTML string `json:"html"`
	} `json:"arms"`
}

func loadOwnedParagraphFixture(t *testing.T, caseID string) ownedParagraphFixture {
	t.Helper()
	raw := readWebResearchTestCorpus(t, "extractor-ablation-input2-2026-09-04.json")
	var input struct {
		Pages []ownedParagraphFixture `json:"pages"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	for _, page := range input.Pages {
		if page.CaseID != caseID {
			continue
		}
		if len(page.Arms) == 0 || page.Arms[0].Name != "go_current" {
			t.Fatal("saved original-HTML baseline is missing")
		}
		digest := sha256.Sum256([]byte(page.Arms[0].HTML))
		if hex.EncodeToString(digest[:]) != page.MarkupSHA {
			t.Fatal("saved original HTML hash mismatch")
		}
		return page
	}
	t.Fatalf("saved case %q is missing", caseID)
	return ownedParagraphFixture{}
}

func TestOwnedParagraphRetainsLateRestrictionInOneBlock(t *testing.T) {
	const owner = "Aurora Observatory"
	const start = "Aurora observatory access is available. "
	const end = "Admission is not available on Saturdays; only members may enter."
	paragraph := start + strings.Repeat("A detailed account of the grounds. ", 16) + end
	if utf8.RuneCountInString(paragraph) <= 500 {
		t.Fatal("fixture no longer places its restriction past the old window")
	}
	markup := `<main><h1>` + owner + `</h1><p>` + paragraph + `</p></main>`
	want := "Section: " + owner + "\n" + paragraph
	if got := selectQueryChunks("Aurora observatory access admission", extractReadableText(markup)); got != want {
		t.Fatalf("complete owned paragraph was fragmented or lost its late restriction: %q", got)
	}
}

func TestOwnedParagraphSourceFormattingKeepsPREAndExplicitBreaks(t *testing.T) {
	markup := "<h1>Aurora rules</h1><p>Public access is available.\r\nOnly registered visitors may enter.</p><pre>" +
		"// First comment line.\n// Continuation belongs to it.\n//\n// Second comment paragraph.\nfunc Example() {}\n" +
		"literal first line\nliteral second line\n</pre><p>Separate first line.<br>Separate second line.</p>"
	text := extractReadableText(markup)
	for _, preserved := range []string{
		"Public access is available. Only registered visitors may enter.",
		"First comment line. Continuation belongs to it.\nSecond comment paragraph.\nfunc Example() {}",
		"literal first line\nliteral second line",
		"Separate first line.\nSeparate second line.",
	} {
		if !strings.Contains(text, preserved) {
			t.Fatalf("formatting normalization lost paragraph, PRE, or explicit BR boundaries: want %q in %q", preserved, text)
		}
	}
	const plain = "Plain first paragraph.\nPlain second paragraph."
	if got := extractReadableText(plain); got != plain {
		t.Fatalf("HTML formatting normalization changed plain-text input: %q", got)
	}
}

func TestOwnedParagraphLimitIncludesEntireOwner(t *testing.T) {
	owner := strings.Repeat("Long owner ", 24) + "Saturday branch only"
	prefix := "Section: " + owner + "\n"
	for _, total := range []int{1000, 1001} {
		paragraph := strings.Repeat("界", total-utf8.RuneCountInString(prefix)-len(" access")) + " access"
		markup := `<h1>` + owner + `</h1><p>` + paragraph + `</p>`
		got := selectQueryChunks("access", extractReadableText(markup))
		if total == 1000 {
			if got != prefix+paragraph || utf8.RuneCountInString(got) != total {
				t.Fatalf("boundary block lost full owner or Unicode text: %q", got)
			}
		} else if got != "" {
			t.Fatalf("oversized owned block emitted an authoritative prefix: %q", got)
		}
	}
}

func TestOwnedParagraphBudgetKeepsLateHighestRankAndFillsRemainingSpace(t *testing.T) {
	pad := func(prefix string, length int) string { return prefix + strings.Repeat("x", length-len(prefix)) }
	// The first two records tie at a lower rank. Document-order budget cutting
	// would fill with them and discard the last, highest-ranked relevant fact.
	early := pad("Aurora early lower-ranked record. ", 780)
	middle := pad("Aurora middle lower-ranked record. ", 780)
	late := pad("Aurora admission schedule confirmed highest-ranked record. ", 850)
	short := "Aurora short additional record."
	text := strings.Join([]string{early, middle, short, late}, "\n")
	got := selectQueryChunks("Aurora admission schedule confirmed", text)
	if got != short+"\n\n"+late {
		t.Fatalf("budget did not prioritize fitting records before document-order rendering: %q", got)
	}
	if utf8.RuneCountInString(got) > maxSnippetLength || len(completePageExcerpts(got, maxSnippetLength)) != 2 {
		t.Fatal("complete paragraph selection exceeded the source budget")
	}
}

func TestOwnedParagraphTableRowRetainsLateConditionOrDropsWhole(t *testing.T) {
	const ending = "Access requires prior authorization; this is not public admission."
	for _, repeats := range []int{15, 40} {
		markup := `<h1>Aurora admission</h1><table><tr><td>North campus</td><td>` + strings.Repeat("Grounds information. ", repeats) + ending + `</td></tr></table>`
		text := extractReadableText(markup)
		got := selectQueryChunks("Aurora North campus admission", text)
		if repeats == 15 {
			if !strings.Contains(got, "Table row: North campus |") || !strings.Contains(got, ending) {
				t.Fatalf("bounded table row lost its owner or condition: %q", got)
			}
		} else {
			// Explicitly push the record over the complete-block cap.
			text += strings.Repeat(" continued", 30)
			if got := selectQueryChunks("Aurora North campus admission", text); got != "" {
				t.Fatalf("oversized table row emitted a partial record: %q", got)
			}
		}
	}
}

func TestOwnedParagraphCapturedGoKeepsSelectedConditionsComplete(t *testing.T) {
	page := loadOwnedParagraphFixture(t, "go_http_client_timeout_body")
	text := extractReadableText(page.Arms[0].HTML)
	for _, test := range []struct {
		query, start, ending string
	}{
		{"Client CheckRedirect ErrUseLastResponse body closed error", "CheckRedirect specifies the policy", "along with a nil error."},
		{"ResponseWriter HTTP/2 client concurrent writing compatibility", "Depending on the HTTP protocol version and the client", "Handlers should read before writing if possible to maximize compatibility."},
	} {
		var paragraph string
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, test.start) {
				paragraph = line
				break
			}
		}
		if utf8.RuneCountInString(paragraph) <= 500 || !strings.HasSuffix(paragraph, test.ending) {
			t.Fatal("saved paragraph no longer demonstrates the original long-condition fixture")
		}
		selected := completePageExcerpts(selectQueryChunks(test.query, text), maxSnippetLength)
		found := false
		for _, block := range selected {
			if strings.Contains(block, test.start) {
				found = true
				if !strings.Contains(block, paragraph) || utf8.RuneCountInString(block) > 1000 {
					t.Fatalf("saved Go paragraph lost its complete original condition: %q", block)
				}
			}
		}
		if !found {
			t.Fatalf("focused saved Go query did not retain the tested paragraph: %q", selected)
		}
	}
}
