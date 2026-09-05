package websearch

import (
	"html"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

func extractReadableText(markup string) string {
	if main := mainElementPattern.FindStringSubmatch(markup); len(main) > 1 {
		markup = main[1]
	}
	markup = discardElementPattern.ReplaceAllString(markup, " ")
	markup = normalizeHTMLSourceNewlines(markup)
	markup, disclosureMarkers := preserveDisclosureSections(markup)
	markup = preserveMenuItemCards(markup)
	markup = preserveMenuItemLinks(markup)
	// In documentation, line-wrapped // comments form one explanation. Keep
	// each contiguous comment paragraph intact without merging across blank
	// comment lines, declarations, or separate code blocks. This is text only.
	markup = preElementPattern.ReplaceAllStringFunc(markup, func(element string) string {
		parts := preElementPattern.FindStringSubmatch(element)
		code := html.UnescapeString(allTagPattern.ReplaceAllString(parts[1], ""))
		var lines, comments []string
		flush := func() {
			if len(comments) > 0 {
				lines = append(lines, strings.Join(comments, " "))
				comments = nil
			}
		}
		for _, line := range strings.Split(code, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "//") {
				comment := strings.TrimSpace(strings.TrimPrefix(line, "//"))
				if comment == "" {
					flush()
				} else {
					comments = append(comments, comment)
				}
			} else {
				flush()
				lines = append(lines, line)
			}
		}
		flush()
		for index, line := range lines {
			// Literal examples/directives in code are data, not the structural
			// markers inserted below from real HTML headings.
			if headingLinePattern.MatchString(line) {
				lines[index] = "Code: " + line
			}
		}
		return "<pre>\n" + html.EscapeString(strings.Join(lines, "\n")) + "\n</pre>"
	})
	// A row's entity may be represented only by an image alt (for example a
	// team logo). Keep cells and their supplied labels together, instead of
	// turning an anonymous transaction into an apparent fact about the query.
	markup = tableRowPattern.ReplaceAllStringFunc(markup, func(row string) string {
		row = tableCellBoundary.ReplaceAllString(row, " | ")
		row = imageElementPattern.ReplaceAllStringFunc(row, func(element string) string {
			label := truncate(strings.TrimSpace(htmlAttributes(element)["alt"]), 120)
			return " " + html.EscapeString(label) + " "
		})
		row = strings.Join(strings.Fields(allTagPattern.ReplaceAllString(row, " ")), " ")
		return "\nTable row: " + row + "\n"
	})
	markup = tableCaptionPattern.ReplaceAllStringFunc(markup, func(element string) string {
		return "\n[Heading 2] " + normalizedHTMLHeading(tableCaptionPattern.FindStringSubmatch(element)[1]) + "\n"
	})
	// Preserve section ownership through HTML stripping. A heading is context
	// for its paragraphs, not an independent fact to concatenate with another
	// team's/person's paragraph selected elsewhere on the page.
	markup = headingElementPattern.ReplaceAllStringFunc(markup, func(element string) string {
		parts := headingElementPattern.FindStringSubmatch(element)
		return "\n[Heading " + parts[1] + "] " + normalizedHTMLHeading(parts[2]) + "\n"
	})
	// Some publishers render section labels as short bold divs rather than
	// semantic headings. Keep those boundaries too, without inferring facts
	// from particular publisher/team names.
	markup = styledLabelPattern.ReplaceAllStringFunc(markup, func(element string) string {
		parts := styledLabelPattern.FindStringSubmatch(element)
		attributes := htmlAttributes(parts[1])
		style := strings.ToLower(attributes["class"] + " " + attributes["style"])
		if strings.Contains(style, "bold") || strings.Contains(style, "font-weight:700") || strings.Contains(style, "font-weight: 700") ||
			strings.Contains(style, "table__title") || strings.Contains(style, "table-title") {
			return "\n[Heading 2] " + normalizedHTMLHeading(parts[2]) + "\n"
		}
		return element
	})
	markup = strongLabelPattern.ReplaceAllStringFunc(markup, func(element string) string {
		return "\n[Heading 3] " + normalizedHTMLHeading(strongLabelPattern.FindStringSubmatch(element)[1]) + "\n"
	})
	markup = blockTagPattern.ReplaceAllString(markup, "\n")
	markup = allTagPattern.ReplaceAllString(markup, " ")
	markup = html.UnescapeString(markup)
	markup = strings.ReplaceAll(markup, "\u00a0", " ")
	markup = spacePattern.ReplaceAllString(markup, " ")

	lines := strings.Split(markup, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if marker, exists := disclosureMarkers[line]; exists {
			line = marker
		} else if strings.HasPrefix(line, "[Disclosure ") {
			line = "Text: " + line // Source literals are never section controls.
		}
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.TrimSpace(newlinePattern.ReplaceAllString(strings.Join(cleaned, "\n"), "\n\n"))
}

// CR/LF outside PRE are source formatting, not HTML block boundaries. Collapse
// them before inserting our structural delimiters; keep actual BR/block tags
// and the PRE line structure used by documentation comment handling intact.
func normalizeHTMLSourceNewlines(markup string) string {
	if !strings.ContainsAny(markup, "\r\n") || !allTagPattern.MatchString(markup) {
		return markup // Preserve plain-text callers' existing line boundaries.
	}
	lineBreaks := strings.NewReplacer("\r", " ", "\n", " ")
	var normalized strings.Builder
	start := 0
	for _, span := range preElementPattern.FindAllStringIndex(markup, -1) {
		normalized.WriteString(lineBreaks.Replace(markup[start:span[0]]))
		normalized.WriteString(markup[span[0]:span[1]])
		start = span[1]
	}
	normalized.WriteString(lineBreaks.Replace(markup[start:]))
	return normalized.String()
}

func normalizedHTMLHeading(markup string) string {
	return html.EscapeString(strings.Join(strings.Fields(html.UnescapeString(allTagPattern.ReplaceAllString(markup, " "))), " "))
}

type textChunk struct {
	text    string
	body    string
	section string
	score   float64
	order   int
}

// Keep selected fetched blocks separate from discovery and complete within the
// existing per-source text budget. The selector already bounds complete owned
// paragraphs; this adds no mid-block truncation or cross-block joining.
func completePageExcerpts(excerpt string, limit int) []string {
	var blocks []string
	used := 0
	for _, block := range strings.Split(excerpt, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		size := utf8.RuneCountInString(block)
		if len(blocks) > 0 {
			size += 2 // Bound a caller's ordinary paragraph-delimited rendering too.
		}
		if used+size > limit {
			continue
		}
		blocks = append(blocks, block)
		used += size
		if len(blocks) == maxChunksPerSource {
			break
		}
	}
	return blocks
}

func selectQueryChunks(query string, text string) string {
	// Keep the frozen production ranker until the offline lexical experiment
	// receives independent review. Candidate ownership and budget are shared.
	return selectRankedChunks(rankLegacyChunks(query, ownedTextChunks(query, text)))
}

func ownedTextChunks(query string, text string) []textChunk {
	terms := queryTerms(query)
	timingQuery, priceQuery := false, false
	for _, term := range terms {
		switch term {
		case "hours", "schedule", "closing", "opening", "tonight", "showtime", "showtimes":
			timingQuery = true
		case "price", "prices", "pricing", "menu", "cost", "budget", "fee", "fees":
			priceQuery = true
		}
	}
	paragraphs := strings.Split(text, "\n")
	candidates := make([]textChunk, 0, len(paragraphs))
	order := 0
	var headings [6]string
	var scopePath []string
	type disclosureScope struct {
		headings      [6]string
		path          []string
		servicePeriod string
		justHeading   bool
	}
	var scopes []disclosureScope
	servicePeriod := ""
	justHeading := false
	for paragraphIndex, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if strings.HasPrefix(paragraph, disclosureStartMarker) {
			if len(scopes) == maxDisclosureDepth {
				return nil // The bounded DOM pass cannot legitimately produce this.
			}
			scopes = append(scopes, disclosureScope{headings, scopePath, servicePeriod, justHeading})
			path := append([]string(nil), scopePath...)
			for _, heading := range headings {
				if heading != "" {
					path = append(path, heading)
				}
			}
			scopePath = append(path, strings.TrimPrefix(paragraph, disclosureStartMarker))
			headings, servicePeriod, justHeading = [6]string{}, "", true
			continue
		}
		if paragraph == disclosureEndMarker {
			if len(scopes) > 0 {
				previous := scopes[len(scopes)-1]
				headings, scopePath, servicePeriod, justHeading = previous.headings, previous.path, previous.servicePeriod, previous.justHeading
				scopes = scopes[:len(scopes)-1]
			}
			continue
		}
		if heading := headingLinePattern.FindStringSubmatch(paragraph); len(heading) > 0 {
			level := int(heading[1][0] - '1')
			headings[level] = heading[2]
			for index := level + 1; index < len(headings); index++ {
				headings[index] = ""
			}
			servicePeriod = ""
			justHeading = true
			continue
		}
		headingPath := append([]string(nil), scopePath...)
		for _, heading := range headings {
			if heading != "" {
				headingPath = append(headingPath, heading)
			}
		}
		section := strings.Join(headingPath, " > ")
		// A literal time range directly between a section heading and its
		// first item applies only within that section. Do not infer service
		// hours from other prose or carry them across a sibling heading.
		if justHeading && section != "" && menuServicePeriodPattern.MatchString(paragraph) &&
			paragraphIndex+1 < len(paragraphs) && strings.HasPrefix(strings.TrimSpace(paragraphs[paragraphIndex+1]), menuItemPrefix) {
			servicePeriod = paragraph
			justHeading = false
			continue
		}
		justHeading = false
		if strings.HasPrefix(paragraph, "Page structured data: ") {
			section = "" // JSON-LD entities do not inherit the last HTML heading.
		}
		// Menus and schedules often put the entire useful value on a short
		// line under an item/section heading. Retain that supplied ownership;
		// never admit an isolated price or clock merely because it is numeric.
		shortOwnedEvidence := section != "" && ((priceQuery && hasPriceEvidence(paragraph)) ||
			(timingQuery && hasTimingEvidence(paragraph)))
		if utf8.RuneCountInString(paragraph) < 12 && !shortOwnedEvidence {
			continue
		}
		lower := strings.ToLower(paragraph)
		if strings.Contains(lower, "all rights reserved") || strings.Contains(lower, "©") ||
			strings.Contains(lower, "cookie policy") || strings.Contains(lower, "privacy policy") {
			continue
		}
		body := paragraph
		if section != "" {
			if servicePeriod != "" && strings.HasPrefix(paragraph, menuItemPrefix) {
				paragraph = "Section service period: " + servicePeriod + "\n" + paragraph
			}
			paragraph = "Section: " + section + "\n" + paragraph
		}
		// A paragraph or table row is one owned unit. Keep its full owner and
		// late exceptions together, or omit the unit; never manufacture an
		// authoritative prefix by cutting at a word, sentence, or row boundary.
		if utf8.RuneCountInString(paragraph) > maxEvidenceBlockLength {
			continue
		}
		candidates = append(candidates, textChunk{text: paragraph, body: body, section: section, order: order})
		order++
	}
	return candidates
}

// Frozen substring scorer, isolated so the lexical experiment can reuse the
// identical complete owned units without duplicating the extraction pipeline.
func rankLegacyChunks(query string, candidates []textChunk) []textChunk {
	terms := queryTerms(query)
	timingQuery, priceQuery := false, false
	for _, term := range terms {
		switch term {
		case "hours", "schedule", "closing", "opening", "tonight", "showtime", "showtimes":
			timingQuery = true
		case "price", "prices", "pricing", "menu", "cost", "budget", "fee", "fees":
			priceQuery = true
		}
	}
	var ranked []textChunk
	for _, candidate := range candidates {
		lower, section := strings.ToLower(candidate.body), strings.ToLower(candidate.section)
		candidate.score = 0
		for _, term := range terms {
			if strings.Contains(lower, term) {
				candidate.score += 3
			}
			if strings.Contains(section, term) {
				candidate.score += 2
			}
		}
		if normalized := strings.ToLower(strings.TrimSpace(query)); normalized != "" && strings.Contains(lower, normalized) {
			candidate.score += 10
		}
		if candidate.score == 0 {
			continue
		}
		if timingQuery && hasTimingEvidence(candidate.body) {
			candidate.score += 8
		}
		if priceQuery {
			if strings.HasPrefix(candidate.body, menuItemPrefix) {
				candidate.score += 12
			} else if hasPriceEvidence(candidate.body) {
				candidate.score += 8
			}
		}
		ranked = append(ranked, candidate)
	}
	sort.SliceStable(ranked, func(left, right int) bool {
		if ranked[left].score == ranked[right].score {
			return ranked[left].order < ranked[right].order
		}
		return ranked[left].score > ranked[right].score
	})
	return ranked
}

func selectRankedChunks(candidates []textChunk) string {
	// Allocate the source budget in rank order. Cutting a document-ordered
	// list later could discard the strongest evidence merely because it was
	// published below weaker paragraphs. Skip whole non-fitting records and
	// keep looking for a smaller ranked record that uses the remaining room.
	fitting := make([]textChunk, 0, maxChunksPerSource)
	used := 0
	for _, candidate := range candidates {
		size := utf8.RuneCountInString(candidate.text)
		if len(fitting) > 0 {
			size += 2
		}
		if used+size > maxSnippetLength {
			continue
		}
		fitting = append(fitting, candidate)
		used += size
		if len(fitting) == maxChunksPerSource {
			break
		}
	}
	// Keep the selected excerpts in document order rather than separating a
	// heading/date from the paragraph it qualifies through score ordering.
	sort.SliceStable(fitting, func(left, right int) bool {
		return fitting[left].order < fitting[right].order
	})
	selected := make([]string, len(fitting))
	for index, candidate := range fitting {
		selected[index] = candidate.text
	}
	return strings.Join(selected, "\n\n")
}

func hasPriceEvidence(text string) bool {
	return priceEvidencePattern.MatchString(text) || freeAdmissionPattern.MatchString(text)
}

var queryStopWords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "are": {}, "for": {}, "from": {},
	"how": {}, "in": {}, "is": {}, "of": {}, "on": {}, "or": {},
	"the": {}, "to": {}, "what": {}, "when": {}, "where": {}, "with": {},
}

func queryTerms(query string) []string {
	words := strings.FieldsFunc(strings.ToLower(query), func(value rune) bool {
		return !unicode.IsLetter(value) && !unicode.IsDigit(value)
	})
	terms := make([]string, 0, len(words))
	seen := make(map[string]struct{}, len(words))
	for _, word := range words {
		if len([]rune(word)) < 2 {
			continue
		}
		if _, ignored := queryStopWords[word]; ignored {
			continue
		}
		if _, exists := seen[word]; exists {
			continue
		}
		seen[word] = struct{}{}
		terms = append(terms, word)
	}
	return terms
}
