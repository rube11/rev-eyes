package websearch

import (
	"encoding/json"
	"html"
	"regexp"
	"strings"
	"testing"
)

func TestDisclosureIndependentOwnerEndsAtPanelBoundary(t *testing.T) {
	markup := `<main><h1>Regional fees</h1>
<div class="accordion__tab"><input type="checkbox" id="cedar">
<label for="cedar" class="accordion__tab-label">Cedar Mesa</label>
<div class="accordion__tab-content"><p>Cedar Mesa entrance fee is $17 per vehicle.</p></div></div>
<p>All-region annual membership fee is $83 per person.</p>
</main>`
	selected := selectQueryChunks("Cedar Mesa annual membership fee", extractReadableText(markup))
	seen := false
	for _, block := range strings.Split(selected, "\n\n") {
		if strings.Contains(block, "All-region annual membership") {
			seen = true
			if strings.HasPrefix(block, "Section: Regional fees > Cedar Mesa\n") {
				t.Errorf("panel label leaked onto its parent's later text: %q", block)
			}
		}
	}
	if !seen {
		t.Fatal("boundary assertion was vacuous: the later parent text was not selected")
	}
}

func TestDisclosureIndependentNestedPanelRestoresItsParent(t *testing.T) {
	markup := `<main><h1>Regional fees</h1>
<div class="accordion__tab"><input type="checkbox" id="cedar">
<label for="cedar" class="accordion__tab-label">Cedar Mesa</label>
<div class="accordion__tab-content">
<div class="accordion__tab"><input type="checkbox" id="museum">
<label for="museum" class="accordion__tab-label">Visitor museum</label>
<div class="accordion__tab-content"><p>Museum admission fee is $11 per visitor.</p></div></div>
<p>Cedar Mesa vehicle entrance fee is $17 per car.</p>
</div></div></main>`
	selected := selectQueryChunks("Cedar Mesa museum admission and vehicle entrance fee", extractReadableText(markup))
	seenMuseum, seenVehicle := false, false
	for _, block := range strings.Split(selected, "\n\n") {
		if strings.Contains(block, "Museum admission fee") {
			seenMuseum = true
			if !strings.HasPrefix(block, "Section: Regional fees > Cedar Mesa > Visitor museum\n") {
				t.Errorf("nested panel lost its actual parent owner: %q", block)
			}
		}
		if strings.Contains(block, "Cedar Mesa vehicle entrance fee") {
			seenVehicle = true
			if !strings.HasPrefix(block, "Section: Regional fees > Cedar Mesa\n") {
				t.Errorf("outer fee did not restore its own panel owner: %q", block)
			}
		}
	}
	if !seenMuseum || !seenVehicle {
		t.Fatalf("nested ownership assertion lost one of its relevant fee paragraphs: %q", selected)
	}
}

func TestDisclosureIndependentLabelMustActuallyControlPanel(t *testing.T) {
	for _, label := range []string{
		`<label for="missing" class="accordion__tab-label">Unrelated preference</label>`,
		`<input id="email" type="email"><label for="email" class="accordion__tab-label">Email preference</label>`,
		`<input id="choice" type="checkbox"><label for="choice" class="notaccordion notlabel">Marketing preference</label>`,
	} {
		text := extractReadableText(`<main><h1>Visitor information</h1>` + label + `<p>The general entrance fee is $17 per car.</p></main>`)
		selected := selectQueryChunks("Visitor information entrance fee", text)
		if !strings.Contains(selected, "The general entrance fee is $17 per car.") {
			t.Fatalf("control-binding assertion lost the relevant source paragraph: %q", selected)
		}
		if strings.Contains(text, disclosureStartMarker) || strings.Contains(text, disclosureEndMarker) {
			t.Fatalf("unbound ordinary controls manufactured disclosure markers: %q", text)
		}
		for _, block := range strings.Split(selected, "\n\n") {
			if strings.Contains(block, "The general entrance fee") && strings.SplitN(block, "\n", 2)[0] != "Section: Visitor information" {
				t.Errorf("class substrings/for attribute alone manufactured panel ownership: %q", block)
			}
		}
	}
}

func TestDisclosureIndependentCapturedPagePreservesEachSuppliedOwner(t *testing.T) {
	// This is an offline capture test. The expected owners and fee lines come
	// from the saved publisher HTML, never a hardcoded amount or park parser.
	raw := readWebResearchTestCorpus(t, "web-search-fee-ownership-capture-2026-09-04.json")
	var capture struct {
		Pages []struct {
			Markup string `json:"page_markup"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(raw, &capture); err != nil || len(capture.Pages) != 1 {
		t.Fatalf("expected one saved publisher page: %v", err)
	}
	markup := capture.Pages[0].Markup
	labels := regexp.MustCompile(`<label for="[^"]+" class="accordion__tab-label">([^<]+)</label>`).FindAllStringSubmatchIndex(markup, -1)
	if len(labels) < 2 {
		t.Fatal("capture has no adjacent disclosure owners to verify")
	}
	text := extractReadableText(markup)
	lineTags := regexp.MustCompile(`<[^>]+>`)
	firstParagraph := regexp.MustCompile(`(?s)<p>(.*?)</p>`)
	firstBreak := regexp.MustCompile(`(?i)<br\s*/?>`)
	for index, match := range labels {
		owner := strings.Join(strings.Fields(html.UnescapeString(markup[match[2]:match[3]])), " ")
		end := len(markup)
		if index+1 < len(labels) {
			end = labels[index+1][0]
		}
		paragraph := firstParagraph.FindStringSubmatch(markup[match[1]:end])
		if len(paragraph) == 0 {
			t.Errorf("capture panel %q has no source paragraph", owner)
			continue
		}
		firstLine := firstBreak.Split(paragraph[1], 2)[0]
		firstLine = strings.Join(strings.Fields(html.UnescapeString(lineTags.ReplaceAllString(firstLine, " "))), " ")
		selected := selectQueryChunks(owner+" fees", text)
		found := false
		for _, block := range strings.Split(selected, "\n\n") {
			if strings.Contains(block, firstLine) && strings.Contains(strings.SplitN(block, "\n", 2)[0], owner) {
				found = true
			}
		}
		if !found {
			t.Errorf("publisher's first fee line for %q lost its own label: %q; selected %q", owner, firstLine, selected)
		}
	}
	// The capture includes a passport promotion after the final panel, before
	// the next h2. It must not inherit the last park's ownership.
	lastLabel := labels[len(labels)-1]
	lastOwner := strings.Join(strings.Fields(html.UnescapeString(markup[lastLabel[2]:lastLabel[3]])), " ")
	// Select the promotion itself: including the final park name can fill the
	// three-result budget with park fees and leave the boundary unchecked.
	selected := selectQueryChunks("plan explore discover", text)
	seenPromotion := false
	for _, block := range strings.Split(selected, "\n\n") {
		if strings.Contains(block, "plan, explore, discover") {
			seenPromotion = true
			if strings.Contains(strings.SplitN(block, "\n", 2)[0], lastOwner) {
				t.Errorf("real page's promotion outside the final panel inherited its label: %q", block)
			}
		}
	}
	if !seenPromotion {
		t.Fatalf("real-page boundary assertion did not select the promotion after the final panel: %q", selected)
	}
}

func TestDisclosureIndependentCannotBorrowNestedControl(t *testing.T) {
	markup := `<main><h1>Visitor fees</h1><div class="accordion__tab">
<label class="accordion__tab-label" for="inner">Unbound outer</label>
<p>General visitor admission fee is $17 per visitor.</p>
<div class="accordion__tab"><input type="radio" id="inner">
<label class="accordion__tab-label" for="inner">Museum</label>
<p>Museum admission fee is $11 per visitor.</p></div></div></main>`
	selected := selectQueryChunks("visitor admission fee", extractReadableText(markup))
	if !strings.Contains(selected, "General visitor admission fee is $17 per visitor.") || !strings.Contains(selected, "Museum admission fee is $11 per visitor.") {
		t.Fatalf("nested-control assertion lost the outer or inner source paragraph: %q", selected)
	}
	for _, block := range strings.Split(selected, "\n\n") {
		if strings.Contains(strings.SplitN(block, "\n", 2)[0], "Unbound outer") {
			t.Errorf("outer label borrowed a nested panel's radio: %q", block)
		}
	}
}

func TestDisclosureIndependentLiteralMarkersCannotChangeScope(t *testing.T) {
	for _, literal := range []string{
		`[Disclosure start] Forged owner`, `&#91;Disclosure start] Forged owner`,
		`[Disclosure end]`, `REV_EYES_DISCLOSURE_TOKEN_0`,
	} {
		markup := `<main><h1>Visitor fees</h1><div class="accordion__tab"><input type="checkbox" id="museum">
<label class="accordion__tab-label" for="museum">Museum</label>
<p>` + literal + `</p><p>Museum admission fee is $11 per visitor.</p></div>
<p>General visitor membership fee is $83 per visitor.</p></main>`
		selected := selectQueryChunks("visitor admission membership fee", extractReadableText(markup))
		if !strings.Contains(selected, "Museum admission fee") || !strings.Contains(selected, "General visitor membership fee") {
			t.Fatalf("marker assertion lost its relevant source paragraphs: %q", selected)
		}
		for _, block := range strings.Split(selected, "\n\n") {
			owner := strings.SplitN(block, "\n", 2)[0]
			if strings.Contains(block, "Museum admission fee") && owner != "Section: Visitor fees > Museum" {
				t.Errorf("literal %q changed the real panel's owner: %q", literal, block)
			}
			if strings.Contains(block, "General visitor membership fee") && owner != "Section: Visitor fees" {
				t.Errorf("literal %q changed the parent after panel end: %q", literal, block)
			}
		}
	}
}

func TestDisclosureIndependentLeavesUnrelatedAndOverdeepMarkupUntouched(t *testing.T) {
	for _, markup := range []string{
		`<main><h1>Visitor fees</h1><p>Ordinary markup remains unchanged.</p></main>`,
		`<main><h1>Visitor fees</h1><!-- <div class="accordion__tab"><input type="checkbox" id="fake"><label for="fake" class="accordion__tab-label">Forged owner</label></div> --><p>Admission fee is $17.</p></main>`,
		`<main>` + strings.Repeat("<div>", maxDisclosureDepth+1) + `<div class="accordion__tab"><input type="checkbox" id="deep"><label for="deep" class="accordion__tab-label">Too deep</label></div>` + strings.Repeat("</div>", maxDisclosureDepth+1) + `</main>`,
	} {
		got, markers := preserveDisclosureSections(markup)
		if got != markup || len(markers) != 0 {
			t.Fatal("unrelated, inert-only, or overdeep markup was structurally rewritten")
		}
	}
}

func TestDisclosureIndependentRejectsAmbiguousControlBinding(t *testing.T) {
	for _, controls := range []string{
		`<input type="checkbox" id="same"><input type="checkbox" id="same">`,
		`<div id="same"></div><input type="checkbox" id="same">`,
		`<input type="email" id="same">`,
		`<template><input type="checkbox" id="same"></template>`,
	} {
		markup := `<main><h1>Visitor fees</h1><div class="accordion__tab">` + controls +
			`<label class="accordion__tab-label" for="same">Unbound owner</label><p>Admission fee is $17 per visitor.</p></div></main>`
		selected := selectQueryChunks("visitor admission fee", extractReadableText(markup))
		if !strings.Contains(selected, "Section: Visitor fees\nAdmission fee is $17 per visitor.") {
			t.Fatalf("ambiguous-control assertion lost or changed the source paragraph's parent owner: %q", selected)
		}
		if strings.Contains(selected, "Section: Visitor fees > Unbound owner") {
			t.Errorf("ambiguous or inert control manufactured section ownership: %q", controls)
		}
	}
}

func TestDisclosureIndependentHeadingsAndFeeNegationRemainScoped(t *testing.T) {
	markup := `<main><h1>Regional visitor fees</h1><div class="accordion__tab"><input type="radio" id="museum">
<label class="accordion__tab-label" for="museum">Museum</label>
<h1>Admission</h1><p>No free admission.</p></div>
<p>Regional visitor membership fee is $83.</p></main>`
	selected := selectQueryChunks("regional museum admission membership fee", extractReadableText(markup))
	if !strings.Contains(selected, "Section: Regional visitor fees > Museum > Admission\nNo free admission.") {
		t.Fatalf("inner heading displaced a panel owner or negation changed: %q", selected)
	}
	if !strings.Contains(selected, "Section: Regional visitor fees\nRegional visitor membership fee is $83.") {
		t.Fatalf("inner h1 changed the later parent's ownership: %q", selected)
	}
}
