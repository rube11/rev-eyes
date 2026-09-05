package openai

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/tool/websearch"
)

const qualitySeasonMonth = `(?:January|February|March|April|May|June|July|August|September|October|November|December|Jan|Feb|Mar|Apr|Jun|Jul|Aug|Sep|Sept|Oct|Nov|Dec)`

var (
	qualitySeasonRange         = regexp.MustCompile(`(?i)\b(` + qualitySeasonMonth + `)\.?\s+([0-9]{1,2})\s*(?:-|–|—|to|through)\s*(` + qualitySeasonMonth + `)\.?\s+([0-9]{1,2})\b`)
	qualitySeasonMonthDot      = regexp.MustCompile(`(?i)\b(` + qualitySeasonMonth + `)\.`)
	qualityReservationPositive = regexp.MustCompile(`(?i)\b(?:needs?\s+(?:one|a\s+(?:timed[- ]entry\s+)?reservation)|(?:reservations?\s+(?:is|are)\s+required)|(?:reservation\s+(?:needed|required))|must\s+(?:reserve|book))\b`)
	qualityReservationNegative = regexp.MustCompile(`(?i)\b(?:do\s+not\s+need|does\s+not\s+need|don['’]t\s+need|not\s+(?:required|needed)|no\s+(?:timed[- ]entry\s+)?reservation)\b`)
)

// The existing scenario asks about tomorrow morning. This independent,
// test-only screen catches one unambiguous contradiction: saying a reservation
// is needed when the cited official requirement season excludes that date.
// Dates come from the source, never a hardcoded expected answer. We cannot infer
// the exact hour meant by "morning", so a within-season NO still needs manual
// review (e.g. a correctly qualified before-8am visit may not require one).
func qualitySeasonalReservationCheck(response string, sources map[string]websearch.Result, asOf time.Time) liveQualityCheck {
	target := asOf.AddDate(0, 0, 1)
	inside, outside := 0, 0
	for _, canonical := range qualityCitedPriceSources(response, sources) {
		source := sources[canonical]
		if !qualityRelevantSource("official_red_rock_entry_guidance", source) {
			continue
		}
		for _, evidence := range qualityEvidenceBlocks(source) {
			for _, block := range qualitySeasonStatements(evidence) {
				normalized := qualityNormalize(block)
				if !qualityHasAny(normalized, "reservation", "reservations") ||
					!qualityHasAny(normalized, "required", "must have") ||
					!qualityHasAny(qualityNormalize(evidence), "scenic drive", "timed entry") ||
					qualityReservationNegative.MatchString(block) {
					continue
				}
				for _, match := range qualitySeasonRange.FindAllStringSubmatch(block, -1) {
					start, startOK := qualitySeasonMonthDay(match[1], match[2])
					end, endOK := qualitySeasonMonthDay(match[3], match[4])
					if !startOK || !endOK {
						continue
					}
					day := int(target.Month())*100 + target.Day()
					inWindow := day >= start && day <= end
					if start > end { // A winter season can cross New Year.
						inWindow = day >= start || day <= end
					}
					if inWindow {
						inside++
					} else {
						outside++
					}
				}
			}
		}
	}
	normalizedResponse := strings.TrimSpace(qualityNormalize(response))
	negative := strings.HasPrefix(normalizedResponse, "no ") || qualityReservationNegative.MatchString(response)
	visitClaim := strings.Contains(normalizedResponse, "tomorrow") || strings.Contains(normalizedResponse, "for your visit") || strings.HasPrefix(normalizedResponse, "you need ") || strings.HasPrefix(normalizedResponse, "you must ")
	affirmative := !negative && (strings.HasPrefix(normalizedResponse, "yes ") || (visitClaim && qualityReservationPositive.MatchString(response)))
	// Conflicting captured seasons are left to explicit manual review. They do
	// not provide an unambiguous outside-season contradiction by themselves.
	contradiction := affirmative && outside > 0 && inside == 0
	return liveQualityCheck{
		Name: "seasonal_reservation_answer_consistency", Passed: !contradiction,
		Detail: fmt.Sprintf("visit date %s; cited official requirement windows: %d include date, %d exclude date; affirmative reservation claim=%t. Outside-season affirmative contradiction=%t. Source-derived date screening does not prove within-season hours or resolve conflicting/unknown rules.", target.Format("2006-01-02"), inside, outside, affirmative, contradiction),
	}
}

func qualitySeasonStatements(snippet string) []string {
	// Month/clock abbreviation periods are not sentence boundaries. Bind the
	// requirement and season within one statement rather than borrowing a date
	// range from an unrelated activity elsewhere in a captured paragraph.
	snippet = qualitySeasonMonthDot.ReplaceAllString(snippet, "$1")
	snippet = strings.NewReplacer("a.m.", "am", "p.m.", "pm", "A.M.", "AM", "P.M.", "PM").Replace(snippet)
	return strings.FieldsFunc(snippet, func(r rune) bool { return r == '.' || r == '!' || r == '?' || r == '\n' })
}

func qualitySeasonMonthDay(monthText, dayText string) (int, bool) {
	monthText = strings.ToLower(monthText)
	day, err := strconv.Atoi(dayText)
	if err != nil || day < 1 {
		return 0, false
	}
	for month := time.January; month <= time.December; month++ {
		if !strings.HasPrefix(strings.ToLower(month.String()), monthText) && !(month == time.September && monthText == "sept") {
			continue
		}
		// Leap year permits a valid annual February 29 boundary without rolling
		// malformed dates such as February 31 into a different month.
		parsed := time.Date(2000, month, day, 0, 0, 0, 0, time.UTC)
		if parsed.Month() != month {
			return 0, false
		}
		return int(month)*100 + day, true
	}
	return 0, false
}
