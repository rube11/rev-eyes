package openai

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Extract numeric components even when clocks are written without a space
// ("8am"). Currency receives an additional unit-specific check below.
var evidenceNumber = regexp.MustCompile(`\d+(?:\.\d+)?`)
var evidenceClock = regexp.MustCompile(`(?i)\b(\d{1,2})(?::(\d{2}))?\s*([ap])\.?m\.?`)
var evidencePrice = regexp.MustCompile(`\$\s*([0-9]+(?:,[0-9]{3})*(?:\.[0-9]+)?)(?:\s*[-–—]\s*\$?\s*([0-9]+(?:,[0-9]{3})*(?:\.[0-9]+)?))?`)
var evidenceYear = regexp.MustCompile(`\b20[0-9]{2}\b`)
var evidenceTimestamp = regexp.MustCompile(`(?i)\b\d{4}-\d{2}-\d{2}t\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:z|[+-]\d{2}:\d{2})\b`)
var structuredEventType = regexp.MustCompile(`(?i)^page structured data: [a-z]*event\s`)
var structuredEventStart = regexp.MustCompile(`(?i)(?:^|;\s*)startDate=([^;\s]+)`)
var structuredEventEnd = regexp.MustCompile(`(?i)(?:^|;\s*)endDate=([^;\s]+)`)
var evidenceUncertainEvent = regexp.MustCompile(`\b(?:will|would|could|may|might|not|never|if)\b`)
var evidenceMonthDayRange = regexp.MustCompile(`(?i)\b(jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|jun(?:e)?|jul(?:y)?|aug(?:ust)?|sep(?:t(?:ember)?)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)\.?\s*(\d{1,2})\s*(?:-|–|—|to|through)\s*(jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|jun(?:e)?|jul(?:y)?|aug(?:ust)?|sep(?:t(?:ember)?)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)\.?\s*(\d{1,2})\b`)

func evidenceMonth(name string) time.Month {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	for month := time.January; month <= time.December; month++ {
		full := strings.ToLower(month.String())
		if name == full || (len(name) >= 3 && strings.HasPrefix(full, name)) {
			return month
		}
	}
	return 0
}

func inRecurringDateRange(date time.Time, match []string) (bool, bool) {
	if len(match) != 5 {
		return false, false
	}
	startMonth, endMonth := evidenceMonth(match[1]), evidenceMonth(match[3])
	startDay, startErr := strconv.Atoi(match[2])
	endDay, endErr := strconv.Atoi(match[4])
	if startMonth == 0 || endMonth == 0 || startErr != nil || endErr != nil || startDay < 1 || startDay > 31 || endDay < 1 || endDay > 31 {
		return false, false
	}
	if time.Date(2000, startMonth, startDay, 0, 0, 0, 0, time.UTC).Month() != startMonth || time.Date(2000, endMonth, endDay, 0, 0, 0, 0, time.UTC).Month() != endMonth {
		return false, false
	}
	value := int(date.Month())*100 + date.Day()
	start, end := int(startMonth)*100+startDay, int(endMonth)*100+endDay
	if start <= end {
		return value >= start && value <= end, true
	}
	return value >= start || value <= end, true // season crosses New Year
}

func contradictsTomorrowReservationSeason(claimText, quote, query string, now time.Time) bool {
	lowerQuery := strings.ToLower(query)
	if !strings.Contains(lowerQuery, "tomorrow") || !strings.Contains(lowerQuery, "reservation") {
		return false
	}
	indices := evidenceMonthDayRange.FindStringIndex(quote)
	if indices == nil {
		return false
	}
	// Bind the range to the requirement's own sentence, not a neighbouring
	// seasonal exhibit, unrelated attraction, or page navigation item.
	before := strings.ToLower(quote[:indices[0]])
	if boundary := strings.LastIndexAny(before, ".!?\n"); boundary >= 0 {
		before = before[boundary+1:]
	}
	if (!strings.Contains(before, "reservation") && !strings.Contains(before, "timed entry")) ||
		(!strings.Contains(before, "required") && !strings.Contains(before, "must")) {
		return false
	}
	match := evidenceMonthDayRange.FindStringSubmatch(quote)
	inside, ok := inRecurringDateRange(now.AddDate(0, 0, 1), match)
	if !ok || inside {
		return false
	}
	claim := strings.ToLower(strings.TrimSpace(claimText))
	// Outside an explicitly quoted requirement season, a positive reservation
	// answer reverses the calendar rule. Fail closed; this does not infer that
	// every other rule or closure is absent.
	positive := strings.HasPrefix(claim, "yes") || strings.Contains(claim, "needs one") || strings.Contains(claim, "need a reservation") || strings.Contains(claim, "reservation is required") || strings.Contains(claim, "reservations are required")
	negative := strings.HasPrefix(claim, "no") || strings.Contains(claim, "do not need") || strings.Contains(claim, "does not need") || strings.Contains(claim, "don't need") || strings.Contains(claim, "no reservation")
	return positive && !negative
}

func claimNumbersSupported(text, quotes string, locations ...*time.Location) bool {
	// Currency is not interchangeable with an age, capacity, date or clock.
	prices := map[float64]bool{}
	for _, match := range evidencePrice.FindAllStringSubmatch(quotes, -1) {
		for _, number := range match[1:] {
			if number != "" {
				value, _ := strconv.ParseFloat(strings.ReplaceAll(number, ",", ""), 64)
				prices[value] = true
			}
		}
	}
	free := strings.Contains(strings.ToLower(quotes), "free") && !strings.Contains(strings.ToLower(quotes), "not free")
	for _, match := range evidencePrice.FindAllStringSubmatch(text, -1) {
		for _, number := range match[1:] {
			if number == "" {
				continue
			}
			value, _ := strconv.ParseFloat(strings.ReplaceAll(number, ",", ""), 64)
			if !prices[value] && !(value == 0 && free) {
				return false
			}
		}
	}
	known := map[float64]bool{}
	for _, item := range evidenceNumber.FindAllString(quotes, -1) {
		value, _ := strconv.ParseFloat(item, 64)
		known[value] = true
	}
	// An offset-qualified instant can be rendered in the user's supplied local
	// timezone and 12-hour notation without inventing a new time or date.
	for _, stamp := range evidenceTimestamp.FindAllString(quotes, -1) {
		parsed, err := time.Parse(time.RFC3339, strings.ToUpper(stamp))
		if err != nil {
			continue
		}
		if len(locations) > 0 && locations[0] != nil {
			parsed = parsed.In(locations[0])
		}
		hour12 := parsed.Hour() % 12
		if hour12 == 0 {
			hour12 = 12
		}
		for _, value := range []int{parsed.Year(), int(parsed.Month()), parsed.Day(), parsed.Hour(), hour12, parsed.Minute(), parsed.Second()} {
			known[float64(value)] = true
		}
	}
	for _, item := range evidenceNumber.FindAllString(text, -1) {
		value, _ := strconv.ParseFloat(item, 64)
		if !known[value] && !(value == 0 && free) {
			return false
		}
	}
	return true
}

func explicitEvidenceDate(quote string, date time.Time) bool {
	for _, stamp := range evidenceTimestamp.FindAllString(quote, -1) {
		if parsed, err := time.Parse(time.RFC3339, strings.ToUpper(stamp)); err == nil && parsed.In(date.Location()).Format("2006-01-02") == date.Format("2006-01-02") {
			return true
		}
	}
	normalize := func(text string) string {
		return strings.Join(strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), " ")
	}
	text := " " + normalize(quote) + " "
	for _, layout := range []string{"2006-01-02", "January 2 2006", "January 02 2006", "Jan 2 2006", "Jan 02 2006", "1/2/2006", "01/02/2006"} {
		if strings.Contains(text, " "+normalize(date.Format(layout))+" ") {
			return true
		}
	}
	return false
}

func weekStart(now time.Time) time.Time {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return day.AddDate(0, 0, -(int(day.Weekday())+6)%7)
}

// Resolve only an explicit individual event's same-publication-day weekday.
// Never treat publication metadata alone as the date of every item on a page.
// Requiring agreement between UTC and local calendar dates avoids guessing
// which publisher timezone an unqualified weekday refers to.
func sameDayAnnouncementDate(sources []webEvidenceSource, claim webEvidenceClaim, date time.Time) bool {
	quote := normalizeEvidence(claim.DateQuote)
	if strings.Contains(quote, "last ") || strings.Contains(quote, "next ") || evidenceUncertainEvent.MatchString(quote) {
		return false
	}
	weekday := strings.ToLower(date.Weekday().String())
	pattern := regexp.MustCompile(`\b(?:announced|signed|released|acquired|waived|traded|opened|closed) (?:on )?` + weekday + `\b`)
	if !pattern.MatchString(quote) {
		return false
	}
	fetched := hasFetchedEvidence(sources)
	for _, source := range sources {
		if fetched && !hasFetchedEvidence([]webEvidenceSource{source}) {
			continue
		}
		if commonEvidenceFragment([]webEvidenceSource{source}, claim.SupportQuote, claim.DateQuote) == "" {
			continue
		}
		publicationDate := source.PublishedDate
		if fetched {
			publicationDate = source.PagePublishedDate
		}
		published, err := time.Parse(time.RFC3339, publicationDate)
		if err != nil {
			continue // Date-only metadata lacks a timezone; keep this path narrow.
		}
		if published.UTC().Format("2006-01-02") == date.Format("2006-01-02") && published.In(date.Location()).Format("2006-01-02") == date.Format("2006-01-02") {
			return true
		}
	}
	return false
}

func hasClock(quote string, value time.Time) bool {
	for _, stamp := range evidenceTimestamp.FindAllString(quote, -1) {
		if parsed, err := time.Parse(time.RFC3339, strings.ToUpper(stamp)); err == nil && parsed.Equal(value) {
			return true
		}
	}
	for _, match := range evidenceClock.FindAllStringSubmatch(quote, -1) {
		hour, _ := strconv.Atoi(match[1])
		minute, _ := strconv.Atoi(match[2])
		if hour < 1 || hour > 12 || minute > 59 {
			continue
		}
		hour %= 12
		if strings.EqualFold(match[3], "p") {
			hour += 12
		}
		if hour == value.Hour() && minute == value.Minute() {
			return true
		}
	}
	return false
}
