package websearch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// These synthetic fixtures exercise complete records, not semantic answer
// quality. The final test separately replays an unchanged saved HTML capture.
func TestCompleteBlocksIndependentPreservesLateRestrictionAndUnicode(t *testing.T) {
	body := "Harbor Preserve park grounds open daily from sunrise to sunset. " +
		strings.Repeat("Trail maps describe the marked paths and overlooks. ", 10) +
		"The observation sign reads 星空. Visitors may not enter the restricted ridge after sunset, including on foot."
	want := "Section: Harbor Preserve > Park grounds\n" + body
	if size := utf8.RuneCountInString(want); size <= 500 || size > 1000 {
		t.Fatalf("fixture must exercise a complete 501-1000-rune block, got %d", size)
	}
	got := independentCompleteBlocks("Harbor Preserve park grounds hours", `<main><h1>Harbor Preserve</h1><h2>Park grounds</h2><p>`+body+`</p></main>`)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("complete owned paragraph lost its late restriction or Unicode: %#v", got)
	}
	assertIndependentCompleteBounds(t, got)
}

func TestCompleteBlocksIndependentDoesNotSplitPrettyPrintedParagraph(t *testing.T) {
	const opening = "Harbor Preserve park grounds open daily from sunrise to sunset."
	const restriction = "After sunset only registered campers may remain in designated campsites; public trail access is prohibited."
	// A source-code newline inside P is ordinary HTML whitespace, not a
	// paragraph boundary. Keep this distinct from BR or separate block tags.
	markup := "<main><h1>Harbor Preserve</h1><h2>Park grounds</h2><p>" + opening + "\n    " + restriction + "</p></main>"
	got := independentCompleteBlocks("Harbor Preserve park grounds hours", markup)
	want := "Section: Harbor Preserve > Park grounds\n" + opening + " " + restriction
	if len(got) != 1 || got[0] != want {
		t.Fatalf("ordinary source whitespace detached a same-paragraph access restriction: %#v", got)
	}
	assertIndependentCompleteBounds(t, got)
}

func TestCompleteBlocksIndependentKeepsParkAndOfficeSeparate(t *testing.T) {
	park := "Park grounds open from sunrise to sunset. " + strings.Repeat("Use only the signed public walking routes. ", 10) +
		"After sunset only registered campers may remain in the designated campground."
	office := "Administrative office hours are Monday-Friday 9am to 4pm. " + strings.Repeat("Permit enquiries are handled by the office staff. ", 10) +
		"The office is closed on state holidays; these are not the park grounds access hours."
	markup := `<main><h1>Harbor Preserve</h1><h2>Park grounds</h2><p>` + park + `</p><h2>Administrative office</h2><p>` + office + `</p></main>`
	got := independentCompleteBlocks("Harbor Preserve park office hours", markup)
	want := []string{"Section: Harbor Preserve > Park grounds\n" + park, "Section: Harbor Preserve > Administrative office\n" + office}
	if len(got) != len(want) {
		t.Fatalf("distinct complete facility records were lost: %#v", got)
	}
	for index := range want {
		if utf8.RuneCountInString(want[index]) <= 500 || got[index] != want[index] {
			t.Fatalf("facility ownership/restriction changed in block %d: %q", index, got[index])
		}
	}
	assertIndependentCompleteBounds(t, got)
}

func TestCompleteBlocksIndependentDropsOversizedRecordsWhole(t *testing.T) {
	long := strings.Repeat("Permit holders must observe every access restriction. ", 24) + "No public admission is permitted."
	for _, test := range []struct{ name, record, forbidden string }{
		{"paragraph", `<p>RESTRICTED_PARAGRAPH admission is $15. ` + long + `</p>`, "RESTRICTED_PARAGRAPH"},
		{"table_row", `<table><tr><td>RESTRICTED_TABLE</td><td>Admission is $15. ` + long + `</td></tr></table>`, "RESTRICTED_TABLE"},
		{"menu_card", `<section class="menu-item"><h3>RESTRICTED_CARD curry</h3><span class="item-price">15.00</span><p>` + long + `</p></section>`, "RESTRICTED_CARD"},
	} {
		t.Run(test.name, func(t *testing.T) {
			const survivor = "Public trail maps remain available at the visitor kiosk."
			markup := `<main><h1>Harbor Preserve</h1><h2>Restricted access</h2>` + test.record + `<h2>Public access</h2><p>` + survivor + `</p></main>`
			got := independentCompleteBlocks("Harbor Preserve permit admission price public trail maps menu", markup)
			want := "Section: Harbor Preserve > Public access\n" + survivor
			if len(got) != 1 || got[0] != want || strings.Contains(strings.Join(got, "\n"), test.forbidden) {
				t.Fatalf("oversized record emitted an incomplete prefix/tail or lost valid sibling: %#v", got)
			}
			assertIndependentCompleteBounds(t, got)
		})
	}
}

func TestCompleteBlocksIndependentIncludesOwnerInBlockLimit(t *testing.T) {
	const heading = "Harbor Preserve restricted maintenance trail"
	body := "Harbor Preserve admission is $15. " + strings.Repeat("界", 950)
	if utf8.RuneCountInString(body) >= 1000 || utf8.RuneCountInString("Section: "+heading+"\n"+body) <= 1000 {
		t.Fatal("fixture must fit without its owner but exceed the complete-block limit")
	}
	markup := `<main><h1>` + heading + `</h1><p>` + body + `</p><h1>Harbor Preserve public trail</h1><p>Harbor Preserve public trail access is free.</p></main>`
	got := independentCompleteBlocks("Harbor Preserve admission price access", markup)
	if len(got) != 1 || got[0] != "Section: Harbor Preserve public trail\nHarbor Preserve public trail access is free." {
		t.Fatalf("oversized owner-plus-body was truncated or admitted: %#v", got)
	}
	assertIndependentCompleteBounds(t, got)
}

func TestCompleteBlocksIndependentPreservesLongOwningHeading(t *testing.T) {
	heading := "Harbor Preserve " + strings.Repeat("maintenance access corridor ", 10) + "registered staff only; not the public entrance"
	const body = "The gate opens daily from 6am to 8pm."
	want := "Section: " + heading + "\n" + body
	if utf8.RuneCountInString(heading) <= 240 || utf8.RuneCountInString(want) > 1000 {
		t.Fatal("fixture must exercise an untruncated long owner within the block cap")
	}
	got := independentCompleteBlocks("Harbor Preserve gate hours", `<main><h1>`+heading+`</h1><p>`+body+`</p></main>`)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("owning heading lost its late public-access restriction: %#v", got)
	}
	assertIndependentCompleteBounds(t, got)
}

func TestCompleteBlocksIndependentFitsPriorityBeforeDocumentOrder(t *testing.T) {
	// Each early block fits alone, and both together nearly fill the source.
	// The late price block must be budgeted before lower-ranked early prose.
	early := func(name string) string {
		return strings.TrimSpace(name + " Harbor Cafe welcomes visitors. " + strings.Repeat("Our dining room has comfortable chairs. ", 18))
	}
	first, second := early("FIRST"), early("SECOND")
	const late = "Harbor Cafe menu prices: lentil stew $17.25 per portion; tax and tip are additional."
	all := "Section: Harbor Cafe\n" + first + "\n\nSection: Harbor Cafe\n" + second + "\n\nSection: Harbor Cafe > Dinner menu\n" + late
	if utf8.RuneCountInString(all) <= 1600 {
		t.Fatal("fixture must force priority-based fitting under the total budget")
	}
	markup := `<main><h1>Harbor Cafe</h1><p>` + first + `</p><p>` + second + `</p><h2>Dinner menu</h2><p>` + late + `</p></main>`
	got := independentCompleteBlocks("Harbor Cafe menu prices", markup)
	if len(got) != 2 || got[0] != "Section: Harbor Cafe\n"+first || got[1] != "Section: Harbor Cafe > Dinner menu\n"+late {
		t.Fatalf("document order crowded out the higher-priority late price or changed stable tie handling: %#v", got)
	}
	assertIndependentCompleteBounds(t, got)
}

func TestCompleteBlocksIndependentRetainsThreeBlockAndTotalRuneBounds(t *testing.T) {
	markup := `<main><h1>Harbor Preserve</h1>`
	for _, marker := range []string{"FIRST", "SECOND", "THIRD", "FOURTH", "FIFTH"} {
		markup += `<p>` + marker + ` Harbor Preserve public trail maps are available at the kiosk.</p>`
	}
	got := independentCompleteBlocks("Harbor Preserve public trail maps", markup+`</main>`)
	if len(got) != 3 || !strings.Contains(got[0], "FIRST") || !strings.Contains(got[2], "THIRD") || strings.Contains(strings.Join(got, "\n"), "FOURTH") {
		t.Fatalf("three-block cap or stable selection changed: %#v", got)
	}
	assertIndependentCompleteBounds(t, got)
}

func TestCompleteBlocksIndependentCapturedParkRestrictions(t *testing.T) {
	// Actual saved HTML, not a reconstructed markup fixture. The closure
	// query below is a targeted extraction diagnostic, not a model/search run.
	data := readWebResearchTestCorpus(t, "extractor-ablation-input2-2026-09-04.json")
	var fixture struct {
		Pages []struct {
			URL           string `json:"url"`
			MarkupSHA     string `json:"markup_sha256"`
			CapturedQuery string `json:"last_captured_search_query"`
			Arms          []struct{ Name, HTML string }
		} `json:"pages"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, page := range fixture.Pages {
		if page.URL != "https://parks.nv.gov/parks/valley-of-fire" {
			continue
		}
		for _, arm := range page.Arms {
			if arm.Name != "go_current" {
				continue
			}
			digest := sha256.Sum256([]byte(arm.HTML))
			if hex.EncodeToString(digest[:]) != page.MarkupSHA {
				t.Fatal("saved original park HTML hash changed")
			}
			text := extractReadableText(arm.HTML)
			var closure string
			for _, paragraph := range strings.Split(text, "\n") {
				if strings.HasPrefix(paragraph, "Annual Maintenance Closure:") {
					closure = paragraph
				}
			}
			if utf8.RuneCountInString(closure) <= 500 || !strings.HasSuffix(closure, "are prohibited.") {
				t.Fatal("saved capture no longer exercises the full late closure restriction")
			}
			closureBlocks := completePageExcerpts(selectQueryChunks("Valley of Fire annual maintenance closure visitors camping commercial activities", text), 1600)
			foundClosure := false
			for _, block := range closureBlocks {
				if strings.Contains(block, "Annual Maintenance Closure:") {
					foundClosure = strings.HasSuffix(block, "\n"+closure) && strings.Contains(block, "including those entering on foot") && strings.Contains(block, "all commercial activities")
				}
			}
			if !foundClosure {
				t.Fatalf("actual closure paragraph lost its late access/commercial restrictions: %#v", closureBlocks)
			}
			hours := completePageExcerpts(selectQueryChunks(page.CapturedQuery, text), 1600)
			park, office := false, false
			for _, block := range hours {
				if strings.Contains(block, "Open Daily Sunrise to Sunset") {
					park = strings.HasPrefix(block, "Section: About Valley of Fire > Park Detail > Hours\n") && !strings.Contains(block, "Office Hours")
				}
				if strings.Contains(block, "Office Hours") {
					office = strings.Contains(block, "Monday-Friday 9 a.m. - 4 p.m. Closed on state holidays.") && !strings.Contains(block, "Park Detail > Hours") && !strings.Contains(block, "Open Daily Sunrise to Sunset")
				}
			}
			if !park || !office {
				t.Fatalf("actual captured-query replay lost the park/office distinction: %#v", hours)
			}
			assertIndependentCompleteBounds(t, closureBlocks)
			assertIndependentCompleteBounds(t, hours)
			return
		}
	}
	t.Fatal("saved original park page missing")
}

func independentCompleteBlocks(query, markup string) []string {
	return completePageExcerpts(selectQueryChunks(query, extractReadableText(markup)), 1600)
}

func assertIndependentCompleteBounds(t *testing.T, blocks []string) {
	t.Helper()
	if len(blocks) == 0 || len(blocks) > 3 || utf8.RuneCountInString(strings.Join(blocks, "\n\n")) > 1600 {
		t.Fatalf("invalid nonempty three-block/1600-rune source: %#v", blocks)
	}
	for _, block := range blocks {
		if !utf8.ValidString(block) || utf8.RuneCountInString(block) > 1000 || strings.Contains(block, "…") {
			t.Fatalf("block is invalid, oversized, or an incomplete ellipsis prefix: %q", block)
		}
	}
}
