package openai

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode/utf8"

	"github.com/rube11/rev-eyes/backend/internal/tool/websearch"
)

// These checks screen out known retrieval/provenance failures. They are NOT an
// entailment grader: passing does not establish that each recommendation, price,
// date, reservation rule, or citation actually supports the model's claims.
// Keep manual claim review separate from search-plan compliance, following:
// https://developers.openai.com/api/docs/guides/evaluation-best-practices
type liveQualityAssessment struct {
	MinimumGatePassed    bool               `json:"minimum_gate_passed"`
	Checks               []liveQualityCheck `json:"checks"`
	ManualReviewRequired []string           `json:"manual_review_required"`
}

type liveQualityCheck struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

func evaluateLiveSearchQuality(scenario liveEyesWebScenario, response string, searches []liveComparisonSearch, asOf time.Time) liveQualityAssessment {
	assessment := liveQualityAssessment{ManualReviewRequired: []string{
		"Heuristic screening only: a person must verify that every concrete answer claim is entailed by its cited retrieved text, not by model memory or navigation text.",
	}}
	add := func(name string, passed bool, detail string) {
		assessment.Checks = append(assessment.Checks, liveQualityCheck{Name: name, Passed: passed, Detail: detail})
	}
	runes := utf8.RuneCountInString(response)
	add("response_fits_glasses", runes > 0 && runes <= 420,
		fmt.Sprintf("%d Unicode characters; the existing glasses contract requires a nonempty answer of at most 420, without silently truncating claims or caveats", runes))
	location, _ := time.LoadLocation("America/Los_Angeles") // Embedded tzdata makes this deterministic on Windows too.
	asOf = asOf.In(location)
	sources := make(map[string]websearch.Result)
	domainViolations := make([]string, 0)
	invalidPayloads := 0
	for _, search := range searches {
		if search.Error != "" {
			continue // A later successful search can recover; errors remain in the raw report.
		}
		var arguments liveSearchArguments
		var payload struct {
			Results []websearch.Result `json:"results"`
		}
		if json.Unmarshal(search.Arguments, &arguments) != nil || json.Unmarshal([]byte(search.Content), &payload) != nil {
			invalidPayloads++
			continue
		}
		queryDomains := make([]string, 0)
		// Also catch explicit positive site: filters in the query. Negative site:
		// expressions are deliberately excluded; this is not a query parser.
		for _, match := range qualitySiteFilter.FindAllStringSubmatch(arguments.Query, -1) {
			queryDomains = append(queryDomains, match[1])
		}
		for _, result := range payload.Results {
			canonical := qualityCanonicalURL(result.URL)
			if canonical == "" || strings.TrimSpace(result.Title+result.Snippet+result.DiscoverySnippet+strings.Join(result.PageExcerpts, "")) == "" {
				invalidPayloads++
				continue
			}
			if (len(arguments.IncludeDomains) > 0 && !qualityURLInDomains(result.URL, arguments.IncludeDomains)) ||
				(len(queryDomains) > 0 && !qualityURLInDomains(result.URL, queryDomains)) {
				domainViolations = append(domainViolations, result.URL)
				continue
			}
			// A later failed/quick fetch must not displace fetched evidence for
			// the same URL. Distinct fetched versions still need manual review.
			if prior, exists := sources[canonical]; !exists || len(qualityFetchedBlocks(prior)) == 0 || len(qualityFetchedBlocks(result)) > 0 {
				sources[canonical] = result
			}
		}
	}
	add("readable_search_evidence", invalidPayloads == 0 && len(sources) > 0,
		fmt.Sprintf("%d unique usable sources; %d malformed payloads/results", len(sources), invalidPayloads))
	add("returned_domains_respect_filters", len(domainViolations) == 0,
		fmt.Sprintf("%d results outside requested include_domains/site: filters: %s", len(domainViolations), strings.Join(domainViolations, ", ")))

	relevant := make(map[string]websearch.Result)
	for canonical, result := range sources {
		if qualityRelevantSource(scenario.name, result) {
			relevant[canonical] = result
		}
	}
	add("scenario_relevant_evidence", len(relevant) > 0,
		fmt.Sprintf("%d sources have the required place/topic/authority signals together in an eligible evidence block (legacy title/snippet only when provenance is absent; not proof of factual support)", len(relevant)))

	citations := qualityResponseURLs(response)
	unknownCitations := make([]string, 0)
	citedRelevant := 0
	for _, citation := range citations {
		if _, ok := sources[citation]; !ok {
			unknownCitations = append(unknownCitations, citation)
		}
		if _, ok := relevant[citation]; ok {
			citedRelevant++
		}
	}
	// Eyes' compact glasses contract uses "Name - detail (Source)" rather than
	// long URLs. Accept that format only if the name identifies one retrieved
	// publisher unambiguously; it still does not prove the adjacent claim.
	namedCitations := qualityResponseSourceNames(response)
	for _, name := range namedCitations {
		matched := qualityResolveSourceName(name, sources)
		if len(matched) == 0 {
			unknownCitations = append(unknownCitations, name)
		}
		for _, canonical := range matched {
			if _, ok := relevant[canonical]; ok {
				citedRelevant++
				break
			}
		}
	}
	add("citation_provenance", len(citations)+len(namedCitations) > 0 && len(unknownCitations) == 0 && citedRelevant > 0,
		fmt.Sprintf("%d URL citations; %d named citations; %d resolve to relevant retrieved evidence; %d absent or ambiguous: %s. Named-source matching preserves the compact glasses format; claim support still needs review.", len(citations), len(namedCitations), citedRelevant, len(unknownCitations), strings.Join(unknownCitations, ", ")))
	priceCheck := qualityQuotedPriceCitationCheck(response, sources)
	assessment.Checks = append(assessment.Checks, priceCheck)
	assessment.ManualReviewRequired = append(assessment.ManualReviewRequired,
		"Quoted-price matching checks only that explicit price expressions occur in the adjacent cited publisher's retrieved text. It does not establish the same venue/product, freshness, fees, inventory, or arithmetic, and leaves unrecognized citation/claim formats to manual review.")

	switch scenario.name {
	case "las_vegas_restaurants_for_jolene":
		assessment.ManualReviewRequired = append(assessment.ManualReviewRequired,
			"Verify every named restaurant is in Las Vegas and serves the requested cuisine. Check menu arithmetic for two people near $60 including disclosed taxes/tip/drinks; unsupported budget fit must be explicitly unverified.")
	case "official_red_rock_entry_guidance":
		assessment.Checks = append(assessment.Checks, qualitySeasonalReservationCheck(response, relevant, asOf))
		assessment.ManualReviewRequired = append(assessment.ManualReviewRequired,
			"Read the official Las Vegas Scenic Drive rule and apply its season, hours, and exceptions to tomorrow in America/Los_Angeles. The seasonal contradiction screen does not establish within-season hours, fees, closures, or unrecognized claim/rule wording. A confident yes/no followed by uncertainty is not a supported answer.")
	case "tonight_in_vegas_with_mateo":
		upcoming := 0
		for _, source := range relevant {
			if qualityHasUpcomingShowtime(source, asOf) {
				upcoming++
			}
		}
		add("tonight_not_past_show_evidence", upcoming > 0,
			fmt.Sprintf("%d relevant sources contain a same-date later showtime or an applicable recurring activity with an explicit remaining time range, relative to %s", upcoming, asOf.Format(time.RFC3339)))
		assessment.ManualReviewRequired = append(assessment.ManualReviewRequired,
			"Verify date and future local showtime refer to the same recommended event (not another listing/footer); confirm ticket availability and all-in price under $100 per person, and exclude nightclubs. Date/time text matching cannot prove this.")
	case "confirmed_raiders_news":
		recent := 0
		for _, source := range relevant {
			if qualityRecentlyPublished(source, asOf) {
				recent++
			}
		}
		add("recent_transaction_evidence", recent > 0,
			fmt.Sprintf("%d transaction-related sources have an explicit date within the last seven days, not in the future", recent))
		if qualityHasAny(qualityNormalize(response), "this week") {
			calendarWeekSources := 0
			for _, source := range relevant {
				if qualityHasCurrentWeekSourceDate(source, asOf) {
					calendarWeekSources++
				}
			}
			add("current_calendar_week_source_evidence", calendarWeekSources > 0,
				fmt.Sprintf("answer explicitly says this week; %d relevant sources have a date from local Monday through evaluation time. Publication date still does not prove each transaction's date", calendarWeekSources))
		}
		assessment.ManualReviewRequired = append(assessment.ManualReviewRequired,
			"Verify EACH transaction itself occurred in the claimed window, names/team/action agree with the source, and the source confirms it rather than repeating a rumor. This week is the local Monday-start calendar week, not the last seven days. A recent publication date is not a transaction date and cannot date unrelated discovery/related-content snippets.")
	default:
		add("known_scenario_quality_rubric", false, "New scenarios need an explicit evidence rubric; unknown scenarios cannot silently pass.")
	}
	assessment.MinimumGatePassed = true
	for _, check := range assessment.Checks {
		assessment.MinimumGatePassed = assessment.MinimumGatePassed && check.Passed
	}
	return assessment
}

var (
	qualityURLPattern          = regexp.MustCompile(`https?://[^\s<>"\]\)]+`)
	qualityParentheticalSource = regexp.MustCompile(`\(([^()\n]+)\)`)
	qualityLabeledSource       = regexp.MustCompile(`(?im)\bsources?:[\t ]*([^\r\n]+)`)
	qualitySourceSeparator     = regexp.MustCompile(`\s*[;,]\s*|\s+/\s+`)
	qualityBareSourceHost      = regexp.MustCompile(`(?i)\b(?:[a-z0-9]+(?:-[a-z0-9]+)*\.)+(?:com|org|net|gov|edu|io|co|us)\b`)
	qualitySiteFilter          = regexp.MustCompile(`(?i)(?:^|\s)site:([a-z0-9.-]+)`)
	qualityWordBreaks          = regexp.MustCompile(`[^a-z0-9]+`)
	qualityShowtime            = regexp.MustCompile(`(?i)\b(1[0-2]|0?[1-9])(?::([0-5][0-9]))?\s*([ap])\.?m\.?\b`)
	qualityISODate             = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`)
	qualityUSDate              = regexp.MustCompile(`\b(?:0?[1-9]|1[0-2])/(?:0?[1-9]|[12][0-9]|3[01])/(?:20[0-9]{2}|[0-9]{2})\b`)
	qualityLongDate            = regexp.MustCompile(`(?i)\b(?:January|February|March|April|May|June|July|August|September|October|November|December|Jan|Feb|Mar|Apr|Jun|Jul|Aug|Sep|Sept|Oct|Nov|Dec)\.?\s+\d{1,2},?\s+\d{4}\b`)
	qualityScheduleYear        = regexp.MustCompile(`\b20[0-9]{2}\b`)
	qualityClockRange          = regexp.MustCompile(`(?i)((?:1[0-2]|0?[1-9])(?::[0-5][0-9])?\s*[ap]\.?m\.?|midnight|noon)\s*(?:-|–|—|to|until|through)\s*((?:1[0-2]|0?[1-9])(?::[0-5][0-9])?\s*[ap]\.?m\.?|midnight|noon)`)
)

func qualityRelevantSource(scenario string, source websearch.Result) bool {
	for _, block := range qualityEvidenceBlocks(source) {
		if qualityRelevantBlock(scenario, source.URL, block) {
			return true
		}
	}
	return false
}

// Keep factual screening on one provenance layer. New captures with no fetched
// blocks contain discovery leads, not verified page evidence. Historical rows
// without provenance retain their old interpretation so old reports remain
// readable; this compatibility is not a claim that they were fetched.
func qualityEvidenceBlocks(source websearch.Result) []string {
	if blocks := qualityFetchedBlocks(source); len(blocks) > 0 {
		return blocks
	}
	if qualityHasProvenance(source) {
		return nil
	}
	blocks := strings.Split(source.Snippet, "\n\n")
	for index := range blocks {
		blocks[index] = source.Title + " " + blocks[index]
	}
	return blocks
}

func qualityFetchedBlocks(source websearch.Result) []string {
	var blocks []string
	for _, block := range source.PageExcerpts {
		if strings.TrimSpace(block) != "" {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

func qualityHasProvenance(source websearch.Result) bool {
	return source.ExtractionStatus != "" || source.DiscoverySnippet != "" || source.PagePublishedDate != "" || len(source.PageExcerpts) > 0
}

func qualityRelevantBlock(scenario, endpoint, block string) bool {
	text := qualityNormalize(block)
	lasVegas := qualityHasAny(text, "las vegas", "vegas nevada", "vegas nv")
	switch scenario {
	case "las_vegas_restaurants_for_jolene":
		return lasVegas && qualityHasAny(text, "chinese", "vietnamese", "ramen", "dim sum", "pho") &&
			qualityHasAny(text, "restaurant", "restaurants", "menu", "dining", "dine", "eat", "noodles", "pho", "ramen", "dim sum")
	case "official_red_rock_entry_guidance":
		return qualityURLInDomains(endpoint, []string{"blm.gov", "recreation.gov", "redrockcanyonlv.org"}) &&
			qualityHasAny(text, "red rock canyon") && qualityHasAny(text, "scenic drive", "scenic loop") &&
			qualityHasAny(text, "reservation", "reservations", "timed entry") &&
			(lasVegas || qualityHasAny(text, "national conservation area", "nevada"))
	case "tonight_in_vegas_with_mateo":
		return lasVegas && qualityHasAny(text, "comedy", "stand up", "live music", "concert", "concerts")
	case "confirmed_raiders_news":
		return qualityHasAny(text, "raiders") && qualityHasAny(text,
			"claimed", "signed", "released", "waived", "activated", "acquired", "traded", "placed on", "roster moves", "transactions")
	default:
		return false
	}
}

func qualityNormalize(text string) string {
	return " " + strings.TrimSpace(qualityWordBreaks.ReplaceAllString(strings.ToLower(text), " ")) + " "
}

func qualityHasAny(normalized string, phrases ...string) bool {
	for _, phrase := range phrases {
		if strings.Contains(normalized, qualityNormalize(phrase)) {
			return true
		}
	}
	return false
}

func qualityCanonicalURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" || parsed.User != nil {
		return ""
	}
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	if parsed.Path == "/" {
		parsed.Path = ""
	}
	return parsed.String()
}

func qualityURLInDomains(raw string, domains []string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	for _, domain := range domains {
		domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
		if domain != "" && (host == domain || strings.HasSuffix(host, "."+domain)) {
			return true
		}
	}
	return false
}

func qualityResponseURLs(response string) []string {
	urls := make([]string, 0)
	seen := make(map[string]bool)
	for _, raw := range qualityURLPattern.FindAllString(response, -1) {
		canonical := qualityCanonicalURL(strings.TrimRight(raw, ".,;:!?"))
		if canonical != "" && !seen[canonical] {
			seen[canonical] = true
			urls = append(urls, canonical)
		}
	}
	return urls
}

func qualityResponseSourceNames(response string) []string {
	names := make([]string, 0)
	seen := make(map[string]bool)
	matches := qualityParentheticalSource.FindAllStringSubmatch(response, -1)
	matches = append(matches, qualityLabeledSource.FindAllStringSubmatch(response, -1)...)
	for _, host := range qualityBareSourceHost.FindAllString(response, -1) {
		matches = append(matches, []string{host, host})
	}
	for _, match := range matches {
		// URL citations already have an exact, separate check.
		if strings.Contains(match[1], "http://") || strings.Contains(match[1], "https://") {
			continue
		}
		for _, name := range qualitySourceSeparator.Split(match[1], -1) {
			name = strings.Trim(strings.TrimSpace(name), ".")
			if qualityHasAny(qualityNormalize(name), "not verified", "not fully verified", "not confirmed", "before tax", "before taxes", "before tip") {
				continue // An explicit factual caveat is not a publisher citation.
			}
			// "Source: Raiders.com. Also, NFL.com reported ..." contains
			// prose after the publisher, not a single giant source identity.
			if hosts := qualityBareSourceHost.FindAllString(name, -1); len(hosts) > 0 {
				for _, host := range hosts {
					key := strings.ToLower(host)
					if !seen[key] {
						seen[key] = true
						names = append(names, host)
					}
				}
				continue
			}
			// Numeric parentheticals are commonly prices/showtimes, not sources.
			if name == "" || strings.ContainsAny(name, "$0123456789") {
				continue
			}
			key := strings.ToLower(name)
			if !seen[key] {
				seen[key] = true
				names = append(names, name)
			}
		}
	}
	return names
}

func qualityResolveSourceName(name string, sources map[string]websearch.Result) []string {
	rawName := strings.Trim(strings.TrimSpace(strings.ToLower(name)), ".")
	name = strings.TrimSpace(qualityNormalize(name))
	if name == "" || qualityHasAny(" "+name+" ", "source", "official site", "website") {
		return nil // Generic labels are not traceable publisher identities.
	}
	// A literal publisher hostname must match the source's host, not words in
	// an unrelated title (e.g. "Vegas.com" is not any .com page about Vegas).
	if qualityBareSourceHost.FindString(rawName) == rawName {
		matched := make([]string, 0)
		for canonical := range sources {
			if qualityURLInDomains(canonical, []string{rawName}) {
				matched = append(matched, canonical)
			}
		}
		return matched
	}
	// Publisher brands commonly join words in their DNS label, e.g.
	// "Vivid Seats" -> vividseats.com. A whole matching label identifies the
	// publisher, including www/blog subdomains, only if the suffix agrees.
	brandMatches := make([]string, 0)
	brandDomains := make(map[string]bool)
	for canonical := range sources {
		parsed, _ := url.Parse(canonical)
		labels := strings.Split(strings.ToLower(parsed.Hostname()), ".")
		for index, label := range labels {
			if index < len(labels)-1 && len(label) > 2 && strings.ReplaceAll(name, " ", "") == label {
				brandDomains[strings.Join(labels[index:], ".")] = true
				brandMatches = append(brandMatches, canonical)
			}
		}
	}
	if len(brandDomains) == 1 {
		return brandMatches
	}
	if len(brandDomains) > 1 {
		return nil // Do not merge lookalike brand domains from different owners.
	}
	matched := make([]string, 0)
	hosts := make(map[string]bool)
	for canonical, source := range sources {
		parsed, _ := url.Parse(canonical)
		text := qualityNormalize(parsed.Hostname() + " " + parsed.Path + " " + source.Title)
		allTokens := true
		for _, token := range strings.Fields(name) {
			if !qualityHasAny(text, token) {
				allTokens = false
				break
			}
		}
		if allTokens {
			hosts[strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")] = true
			matched = append(matched, canonical)
		}
	}
	if len(hosts) != 1 {
		return nil
	}
	return matched
}

func qualityHasUpcomingShowtime(source websearch.Result, asOf time.Time) bool {
	for _, block := range qualityEvidenceBlocks(source) {
		// Do not borrow a date from another extracted event/chunk on the page.
		if qualityHasDatedUpcomingShowtime(block, asOf) {
			return true
		}
	}
	return qualityHasRecurringActivityWindow(source, asOf)
}

func qualityHasDatedUpcomingShowtime(text string, asOf time.Time) bool {
	dateMatches := false
	for _, date := range qualityDatesInText(text, asOf.Location()) {
		if date.Format("2006-01-02") == asOf.Format("2006-01-02") {
			dateMatches = true
		}
	}
	if !dateMatches {
		return false
	}
	for _, match := range qualityShowtime.FindAllStringSubmatch(text, -1) {
		var hour, minute int
		fmt.Sscan(match[1], &hour)
		fmt.Sscan(match[2], &minute)
		hour %= 12
		if strings.EqualFold(match[3], "p") {
			hour += 12
		}
		showtime := time.Date(asOf.Year(), asOf.Month(), asOf.Day(), hour, minute, 0, 0, asOf.Location())
		if showtime.After(asOf) {
			return true
		}
	}
	return false
}

// This intentionally accepts only explicit schedules, not "nightly from 6pm"
// with an unknown ending time. A current recurring schedule can establish a
// remaining window without an event-specific calendar date. It is still not
// proof of ticket inventory, price, or that the source is up to date.
func qualityHasRecurringActivityWindow(source websearch.Result, asOf time.Time) bool {
	if !qualityRelevantSource("tonight_in_vegas_with_mateo", source) {
		return false
	}
	for _, block := range qualityEvidenceBlocks(source) {
		text := strings.NewReplacer("a.m.", "am", "p.m.", "pm", "A.M.", "AM", "P.M.", "PM").Replace(block)
		fragments := strings.FieldsFunc(text, func(r rune) bool {
			return r == '\n' || r == ';' || r == '.' || r == '!' || r == '?'
		})
		for _, fragment := range fragments {
			normalized := qualityNormalize(fragment)
			if !qualityHasAny(normalized, "live music", "comedy", "stand up", "concert", "concerts") ||
				qualityHasAny(normalized, "except", "excluding", "closed", "cancelled", "canceled", "no shows",
					"can begin", "can continue", "may run", "recorded music", "music videos", "canopy shows", "video shows", "light shows") {
				continue
			}
			if !qualityRecurringDateApplies(fragment, asOf) {
				continue
			}
			for _, match := range qualityClockRange.FindAllStringSubmatch(fragment, -1) {
				startHour, startMinute, startOK := qualityParseClock(match[1])
				endHour, endMinute, endOK := qualityParseClock(match[2])
				if !startOK || !endOK || (startHour == endHour && startMinute == endMinute) {
					continue
				}
				start := time.Date(asOf.Year(), asOf.Month(), asOf.Day(), startHour, startMinute, 0, 0, asOf.Location())
				end := time.Date(asOf.Year(), asOf.Month(), asOf.Day(), endHour, endMinute, 0, 0, asOf.Location())
				if !end.After(start) {
					end = end.AddDate(0, 0, 1)
				}
				if end.After(asOf) {
					return true
				}
			}
		}
	}
	return false
}

func qualityRecurringDateApplies(fragment string, asOf time.Time) bool {
	normalized := qualityNormalize(fragment)
	weekend := asOf.Weekday() == time.Saturday || asOf.Weekday() == time.Sunday
	if qualityHasAny(normalized, "weekend", "weekends") && !weekend {
		return false
	}
	if qualityHasAny(normalized, "weekday", "weekdays") && weekend {
		return false
	}
	// Explicit historical date restrictions cannot be turned into perpetual
	// daily schedules. Conservatively leave date-range interpretation to review.
	for _, year := range qualityScheduleYear.FindAllString(fragment, -1) {
		if year != asOf.Format("2006") {
			return false
		}
	}
	for month := time.January; month <= time.December; month++ {
		if month != asOf.Month() && qualityHasAny(normalized, month.String(), month.String()[:3]) {
			return false
		}
	}
	for _, date := range qualityDatesInText(fragment, asOf.Location()) {
		if date.Format("2006-01-02") != asOf.Format("2006-01-02") {
			return false
		}
	}
	dayFound := false
	currentDay := false
	for day := time.Sunday; day <= time.Saturday; day++ {
		if qualityHasAny(normalized, day.String(), day.String()+"s", day.String()[:3]) {
			dayFound = true
			currentDay = currentDay || day == asOf.Weekday()
		}
	}
	if dayFound {
		return currentDay
	}
	return qualityHasAny(normalized, "daily", "nightly", "every day", "every night")
}

func qualityParseClock(value string) (hour, minute int, ok bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "midnight":
		return 0, 0, true
	case "noon":
		return 12, 0, true
	}
	match := qualityShowtime.FindStringSubmatch(value)
	if len(match) == 0 {
		return 0, 0, false
	}
	fmt.Sscan(match[1], &hour)
	fmt.Sscan(match[2], &minute)
	hour %= 12
	if strings.EqualFold(match[3], "p") {
		hour += 12
	}
	return hour, minute, true
}

func qualityRecentlyPublished(source websearch.Result, asOf time.Time) bool {
	dates := qualitySourceDates(source, asOf.Location())
	startOfDay := time.Date(asOf.Year(), asOf.Month(), asOf.Day(), 0, 0, 0, 0, asOf.Location())
	for _, date := range dates {
		if !date.Before(startOfDay.AddDate(0, 0, -6)) && !date.After(asOf) {
			return true
		}
	}
	return false
}

func qualityHasCurrentWeekSourceDate(source websearch.Result, asOf time.Time) bool {
	startOfDay := time.Date(asOf.Year(), asOf.Month(), asOf.Day(), 0, 0, 0, 0, asOf.Location())
	daysSinceMonday := (int(asOf.Weekday()) + 6) % 7
	startOfWeek := startOfDay.AddDate(0, 0, -daysSinceMonday)
	for _, date := range qualitySourceDates(source, asOf.Location()) {
		if !date.Before(startOfWeek) && !date.After(asOf) {
			return true
		}
	}
	return false
}

func qualitySourceDates(source websearch.Result, location *time.Location) []time.Time {
	blocks := qualityEvidenceBlocks(source)
	if len(blocks) == 0 {
		return nil
	}
	publicationDate := source.PublishedDate
	if qualityHasProvenance(source) {
		publicationDate = source.PagePublishedDate
	}
	dates := qualityDatesInText(publicationDate, location)
	if timestamp, err := time.Parse(time.RFC3339, publicationDate); err == nil {
		dates = []time.Time{timestamp.In(location)}
	}
	if len(dates) == 0 {
		for _, block := range blocks {
			dates = append(dates, qualityDatesInText(block, location)...)
		}
	}
	return dates
}

func qualityDatesInText(text string, location *time.Location) []time.Time {
	dates := make([]time.Time, 0)
	for _, raw := range qualityISODate.FindAllString(text, -1) {
		if date, err := time.ParseInLocation("2006-01-02", raw, location); err == nil {
			dates = append(dates, date)
		}
	}
	for _, raw := range qualityUSDate.FindAllString(text, -1) {
		for _, layout := range []string{"1/2/2006", "1/2/06"} {
			if date, err := time.ParseInLocation(layout, raw, location); err == nil {
				dates = append(dates, date)
				break
			}
		}
	}
	for _, raw := range qualityLongDate.FindAllString(text, -1) {
		raw = strings.ReplaceAll(strings.ReplaceAll(raw, ",", ""), ".", "")
		raw = strings.ReplaceAll(raw, "Sept ", "Sep ")
		for _, layout := range []string{"January 2 2006", "Jan 2 2006"} {
			if date, err := time.ParseInLocation(layout, raw, location); err == nil {
				dates = append(dates, date)
				break
			}
		}
	}
	return dates
}
