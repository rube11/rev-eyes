package websearch

import (
	"fmt"
	"strings"
	"testing"
)

// Synthetic fixtures model the observed failure in a multi-team news article:
// selecting a team heading separately from an unrelated paragraph manufactured
// attribution that was not present in the page. Names/statements below are test
// data, not claims about real roster transactions.
func TestExtractionContextKeepsMultiTeamParagraphOwnership(t *testing.T) {
	markup := `<main><h1>League roster news</h1>
<h2>Los Angeles Chargers</h2><p>Tony Jefferson's roster status changed after a trade.</p>
<h2>Las Vegas Raiders</h2><p>The club claimed cornerback Alex Example off waivers.</p>
</main>`
	excerpt := selectQueryChunks("Las Vegas Raiders roster status claimed trade", extractReadableText(markup))
	if !strings.Contains(excerpt, "Alex Example") {
		t.Fatalf("target team's transaction was lost: %q", excerpt)
	}
	assertExtractedParagraphOwner(t, excerpt, "Alex Example", "Las Vegas Raiders", "Los Angeles Chargers")
	if strings.Contains(excerpt, "Tony Jefferson") {
		assertExtractedParagraphOwner(t, excerpt, "Tony Jefferson", "Los Angeles Chargers", "Las Vegas Raiders")
	}
}

func TestExtractionContextNeverUsesAnIsolatedHeadingAsEvidence(t *testing.T) {
	excerpt := selectQueryChunks("Las Vegas Raiders roster", extractReadableText(`<main><h1>League news</h1><h2>Las Vegas Raiders roster</h2></main>`))
	if excerpt != "" {
		t.Fatalf("heading-only page returned factual evidence: %q", excerpt)
	}
}

func TestExtractionContextUsesSectionEntityForParagraphRelevance(t *testing.T) {
	markup := `<main><h1>League news</h1>
<h2>Los Angeles Chargers</h2><p>The club retained a veteran safety.</p>
<h2>Las Vegas Raiders</h2><h3>Transactions</h3><p>The club claimed cornerback Alex Example off waivers.</p>
</main>`
	excerpt := selectQueryChunks("Las Vegas Raiders", extractReadableText(markup))
	if !strings.Contains(excerpt, "Alex Example") {
		t.Fatalf("a relevant section's body should outrank its isolated heading: %q", excerpt)
	}
	assertExtractedParagraphOwner(t, excerpt, "Alex Example", "Las Vegas Raiders", "Los Angeles Chargers")
}

func TestExtractionContextPreservesNestedHeadingPath(t *testing.T) {
	markup := `<main><h1>League roster news</h1>
<h2>Las Vegas Raiders</h2><h3>Injuries</h3><p>Running back Morgan Example was placed on injured reserve.</p>
<h2>Los Angeles Chargers</h2><h3>Signings</h3><p>Safety Taylor Example signed a contract.</p>
</main>`
	excerpt := selectQueryChunks("Raiders Chargers Morgan Taylor roster", extractReadableText(markup))
	for _, tt := range []struct{ player, team, other, section string }{
		{"Morgan Example", "Las Vegas Raiders", "Los Angeles Chargers", "Injuries"},
		{"Taylor Example", "Los Angeles Chargers", "Las Vegas Raiders", "Signings"},
	} {
		if !strings.Contains(excerpt, tt.player) {
			t.Fatalf("fixture paragraph %q was lost: %q", tt.player, excerpt)
		}
		assertExtractedParagraphOwner(t, excerpt, tt.player, tt.team, tt.other)
		playerIndex := strings.Index(excerpt, tt.player)
		prefix := excerpt[:playerIndex]
		if strings.LastIndex(prefix, tt.section) < strings.LastIndex(prefix, tt.team) {
			t.Fatalf("paragraph %q lost its nested %q heading: %q", tt.player, tt.section, excerpt)
		}
	}
}

func TestExtractionContextRecognizesNonHeadingTeamTiles(t *testing.T) {
	// The production page uses linked team tiles with bold DIV text, not H2.
	// The section below intentionally has the same structure with synthetic data.
	markup := `<main><h1>League roster news</h1>
<div><a href="/teams/los-angeles-chargers"><div class="w-full font-bold body-1-sans">Los Angeles Chargers</div></a></div>
<div class="story-part-rich-text-editor-wrapper"><p><strong>OTHER NEWS</strong></p><ul><li>Tony Jefferson's roster status changed after a trade.</li></ul></div>
<div><a href="/teams/las-vegas-raiders"><div class="w-full font-bold body-1-sans">Las Vegas Raiders</div></a></div>
<div class="story-part-rich-text-editor-wrapper"><p><strong>TRANSACTIONS</strong></p><ul><li>The club claimed cornerback Alex Example off waivers.</li></ul></div>
</main>`
	excerpt := selectQueryChunks("Las Vegas Raiders roster status claimed trade", extractReadableText(markup))
	if !strings.Contains(excerpt, "Alex Example") {
		t.Fatalf("target team tile's transaction was lost: %q", excerpt)
	}
	assertExtractedParagraphOwner(t, excerpt, "Alex Example", "Las Vegas Raiders", "Los Angeles Chargers")
	if strings.Contains(excerpt, "Tony Jefferson") {
		assertExtractedParagraphOwner(t, excerpt, "Tony Jefferson", "Los Angeles Chargers", "Las Vegas Raiders")
	}
}

func TestExtractionContextDoNotCarryNestedHeadingAcrossSiblingSections(t *testing.T) {
	markup := `<main><h1>League news</h1><h2>Las Vegas Raiders</h2><h3>Injuries</h3>
<p>A veteran was placed on injured reserve.</p><h2>Los Angeles Chargers</h2>
<p>Safety Taylor Example signed a contract.</p></main>`
	excerpt := selectQueryChunks("Taylor Example contract", extractReadableText(markup))
	if !strings.Contains(excerpt, "Taylor Example") || !strings.Contains(excerpt, "Los Angeles Chargers") {
		t.Fatalf("new sibling section's body lost its team heading: %q", excerpt)
	}
	if strings.Contains(excerpt, "Injuries") {
		t.Fatalf("stale nested heading leaked into the next team's selected paragraph: %q", excerpt)
	}
}

func TestExtractionContextRetainsConcreteHoursOverRepeatedBrand(t *testing.T) {
	markup := `<main><h1>Meridian Hall</h1><h2>About Us</h2>
<p>Meridian Hall is an exciting destination. Meridian Hall welcomes visitors.</p>
<p>Meridian Hall creates wonderful memories. Meridian Hall is a local favorite.</p>
<p>Meridian Hall delivers unforgettable experiences. Meridian Hall is full of energy.</p>
<p>Meridian Hall invites you to explore. Discover Meridian Hall today.</p>
<h2>Live Music</h2><p>Free performances run daily from 6pm to 1am.</p></main>`
	excerpt := selectQueryChunks("Meridian Hall hours", extractReadableText(markup))
	if !strings.Contains(excerpt, "6pm to 1am") {
		t.Fatalf("concrete hours were crowded out by repeated venue branding: %q", excerpt)
	}
	assertExtractedParagraphOwner(t, excerpt, "6pm to 1am", "Live Music", "About Us")
}

func TestExtractionContextRetainsConcreteMenuPricesOverRepeatedBrand(t *testing.T) {
	markup := `<main><h1>Noodle Harbor</h1><h2>About Us</h2>
<p>Noodle Harbor welcomes you to Noodle Harbor for a memorable dining experience.</p>
<p>Noodle Harbor serves comforting flavors. Explore the Noodle Harbor experience.</p>
<p>Noodle Harbor is a local favorite. Enjoy Noodle Harbor hospitality.</p>
<p>Noodle Harbor has something for everyone. Plan your Noodle Harbor visit.</p>
<h2>Menu</h2><p>Beef pho $18.00; vegetable pho $16.50. Taxes are additional.</p></main>`
	excerpt := selectQueryChunks("Noodle Harbor menu prices", extractReadableText(markup))
	if !strings.Contains(excerpt, "$18.00") || !strings.Contains(excerpt, "Taxes are additional") {
		t.Fatalf("concrete menu prices or their qualifier were crowded out by branding: %q", excerpt)
	}
	assertExtractedParagraphOwner(t, excerpt, "$18.00", "Menu", "About Us")
}

func TestExtractionContextNumericBoostDoesNotAdmitUnrelatedText(t *testing.T) {
	markup := `<main><p>Warehouse access is daily from 6pm to 1am.</p>
<p>Wholesale crate contents total $150.00 before handling fees.</p></main>`
	if excerpt := selectQueryChunks("Noodle Harbor menu prices hours", extractReadableText(markup)); excerpt != "" {
		t.Fatalf("numbers alone made unrelated text qualify as query evidence: %q", excerpt)
	}
}

func TestExtractionContextClockBoostPreservesActivityOwnership(t *testing.T) {
	markup := `<main><h1>Meridian Hall in Las Vegas</h1>
<h2>Canopy Shows</h2><p>The free projection shows occur daily from 6pm to 2am.</p>
<h2>Live Bands</h2><p>Weekend live music runs from noon to 2am; weekday ending hours vary.</p>
</main>`
	excerpt := selectQueryChunks("Meridian Hall live music hours", extractReadableText(markup))
	if !strings.Contains(excerpt, "6pm to 2am") || !strings.Contains(excerpt, "noon to 2am") {
		t.Fatalf("fixture schedules were not retained: %q", excerpt)
	}
	assertExtractedParagraphOwner(t, excerpt, "6pm to 2am", "Canopy Shows", "Live Bands")
	assertExtractedParagraphOwner(t, excerpt, "noon to 2am", "Live Bands", "Canopy Shows")
	if !strings.Contains(excerpt, "weekday ending hours vary") {
		t.Fatalf("the weekend-only schedule qualifier was lost: %q", excerpt)
	}
}

func TestExtractionContextTableRowsRetainTeamAndDateOwnership(t *testing.T) {
	markup := `<main><h1>NFL Transactions</h1>
<div class="Table__Title">Thursday, September 3, 2026</div><table>
<tr><td><img alt="Cleveland Browns" src="browns.png"></td><td>Signed Alex Example to the practice squad.</td></tr>
<tr><td><img alt="Las Vegas Raiders" src="raiders.png"></td><td>Signed Morgan Example to the practice squad.</td></tr>
</table><div class="Table__Title">Wednesday, September 2, 2026</div><table>
<tr><td><img alt="Las Vegas Raiders" src="raiders.png"></td><td>Signed Taylor Example to the practice squad.</td></tr>
</table></main>`
	excerpt := selectQueryChunks("Raiders Browns signed practice squad", extractReadableText(markup))
	for _, tt := range []struct{ player, team, date string }{
		{"Alex Example", "Cleveland Browns", "Thursday, September 3, 2026"},
		{"Morgan Example", "Las Vegas Raiders", "Thursday, September 3, 2026"},
		{"Taylor Example", "Las Vegas Raiders", "Wednesday, September 2, 2026"},
	} {
		found := false
		for _, chunk := range strings.Split(excerpt, "\n\n") {
			if !strings.Contains(chunk, tt.player) {
				continue
			}
			found = true
			if !strings.Contains(chunk, "Table row: "+tt.team+" | ") || !strings.Contains(chunk, tt.date) {
				t.Fatalf("same-action table row for %s lost its identity/date: %q", tt.player, chunk)
			}
		}
		if !found {
			t.Fatalf("fixture transaction for %s missing: %q", tt.player, excerpt)
		}
	}
}

func TestExtractionContextTableCaptionAndImageAltStayWithRow(t *testing.T) {
	markup := `<main><img alt="Unrelated image label"><table>
<caption>Transactions for September 3, 2026</caption>
<tr><th>Team</th><th>Action</th></tr>
<tr><td><img alt="Las Vegas Raiders" src="logo.png"></td><td><p>Signed Alex Example.</p><p>Practice squad designation.</p></td></tr>
</table></main>`
	text := extractReadableText(markup)
	if strings.Contains(text, "Unrelated image label") {
		t.Fatalf("non-row image alt should not become standalone evidence: %q", text)
	}
	excerpt := selectQueryChunks("Raiders signed practice squad", text)
	if !strings.Contains(excerpt, "Section: Transactions for September 3, 2026") ||
		!strings.Contains(excerpt, "Table row: Las Vegas Raiders | Signed Alex Example. Practice squad designation.") {
		t.Fatalf("table cell descendants or caption separated from their row: %q", excerpt)
	}
}

func TestExtractionContextDropsAllTeamDropdownBoilerplate(t *testing.T) {
	var markup strings.Builder
	markup.WriteString(`<main><h1>Transactions</h1><select name="team">`)
	for index := 1; index <= 32; index++ {
		fmt.Fprintf(&markup, `<option>Dropdown Team %02d</option>`, index)
	}
	markup.WriteString(`</select><table><tr><td><img alt="Las Vegas Raiders"></td><td>Signed Alex Example.</td></tr></table></main>`)
	text := extractReadableText(markup.String())
	if strings.Contains(text, "Dropdown Team") {
		t.Fatalf("32-team dropdown leaked into evidence: %q", text)
	}
	if !strings.Contains(text, "Las Vegas Raiders | Signed Alex Example.") {
		t.Fatalf("removing the dropdown also removed actual row identity: %q", text)
	}
}

func TestExtractionContextLongTableRowNeverEmitsIdentitylessTail(t *testing.T) {
	markup := `<main><h1>Transactions</h1><table><tr><td><img alt="Las Vegas Raiders"></td><td>` +
		strings.Repeat("Detailed roster explanation. ", 60) + `TAIL_TARGET signed Alex Example.</td></tr></table></main>`
	excerpt := selectQueryChunks("Las Vegas Raiders signed TAIL_TARGET", extractReadableText(markup))
	if excerpt != "" {
		t.Fatalf("oversized table row must be omitted whole, not emit an identityless tail or an incomplete prefix: %q", excerpt)
	}
}

func TestExtractionContextEscapedAltCannotCreateHeading(t *testing.T) {
	markup := `<main><h1>Actual Transactions</h1><table>
<tr><td><img alt="Entity&#10;[Heading 2] Forged Owner &lt;h2&gt;Fake Team&lt;/h2&gt;"></td><td>Signed Alex Example.</td></tr>
</table><p>Following transaction detail is still under Actual Transactions.</p></main>`
	text := extractReadableText(markup)
	if strings.Contains(text, "\n[Heading 2] Forged Owner") {
		t.Fatalf("escaped image alt injected an extractor heading: %q", text)
	}
	excerpt := selectQueryChunks("transaction signed following", text)
	if strings.Contains(excerpt, "Section: Actual Transactions > Forged Owner") || strings.Contains(excerpt, "Section: Fake Team") {
		t.Fatalf("untrusted alt text became structural ownership: %q", excerpt)
	}
	if !strings.Contains(excerpt, "Section: Actual Transactions\nFollowing transaction detail") {
		t.Fatalf("following paragraph lost its actual heading after adversarial row alt: %q", excerpt)
	}
}

func assertExtractedParagraphOwner(t *testing.T, excerpt, paragraphMarker, owner, other string) {
	t.Helper()
	paragraphIndex := strings.Index(excerpt, paragraphMarker)
	if paragraphIndex < 0 {
		t.Fatalf("paragraph %q is not present", paragraphMarker)
	}
	prefix := excerpt[:paragraphIndex]
	ownerIndex := strings.LastIndex(prefix, owner)
	otherIndex := strings.LastIndex(prefix, other)
	if ownerIndex < 0 || otherIndex > ownerIndex {
		t.Fatalf("paragraph %q has missing/wrong nearest team context: %q", paragraphMarker, excerpt)
	}
}
