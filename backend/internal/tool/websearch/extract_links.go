package websearch

import (
	"context"
	"html"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
)

const (
	maxEvidenceLinks     = 4
	maxLinkedPageFetches = 2
	maxEvidenceAnchors   = 512
)

var evidenceAnchorPattern = regexp.MustCompile(`(?is)<a\s+([^>]*)>(.*?)</a\s*>`)
var evidenceTitlePattern = regexp.MustCompile(`(?is)<title(?:\s[^>]*)?>(.*?)</title\s*>`)
var evidenceSVGPattern = regexp.MustCompile(`(?is)<svg\b[^>]*>.*?</svg\s*>`)
var evidenceBasePattern = regexp.MustCompile(`(?is)<base\s+([^>]*)>`)

func extractPageTitle(markup string) string {
	markup = htmlCommentPattern.ReplaceAllString(markup, " ")
	markup = inertElementPattern.ReplaceAllString(markup, " ")
	match := evidenceTitlePattern.FindStringSubmatch(markup)
	if len(match) < 2 {
		return ""
	}
	return truncate(strings.Join(strings.Fields(html.UnescapeString(allTagPattern.ReplaceAllString(match[1], " "))), " "), maxTitleLength)
}

type evidenceLink struct {
	url   string
	label string
	score int
}

// Follow only explicit first-party informational links. This is not a crawler,
// URL guesser, JavaScript browser, or ticket/order integration. Query-bearing
// links and action endpoints are deliberately excluded from automatic fetching.
func discoverEvidenceLinks(query string, base *url.URL, markup string) []evidenceLink {
	menu, schedule, hours := false, false, false
	explicitMenu, privateEvent := false, false
	requestedServices := make(map[string]bool)
	for _, term := range queryTerms(query) {
		switch term {
		case "menu", "menus", "price", "prices", "pricing", "budget", "dinner", "lunch", "breakfast", "brunch", "dessert", "desserts":
			menu = true
		case "hours", "opening", "closing":
			hours = true
		case "schedule", "schedules", "showtime", "showtimes", "events", "calendar", "shows", "performance", "performances", "concert", "concerts", "comedy", "ticket", "tickets":
			schedule = true
		}
		switch term {
		case "menu", "menus", "dinner", "lunch", "breakfast", "brunch", "dessert", "desserts", "restaurant", "restaurants", "food":
			explicitMenu = true
		case "meeting", "meetings", "wedding", "weddings", "corporate", "conference", "conferences", "catering":
			privateEvent = true
		}
		if service := menuServiceTerm(term); service != "" {
			requestedServices[service] = true
		}
	}
	// Show ticket prices do not imply restaurant menu intent.
	if schedule && !explicitMenu {
		menu = false
	}
	if base == nil || (!menu && !schedule && !hours) {
		return nil
	}
	markup = htmlCommentPattern.ReplaceAllString(markup, " ")
	markup = inertElementPattern.ReplaceAllString(markup, " ")
	// Do not reinterpret a form control as a research link.
	markup = evidenceFormPattern.ReplaceAllString(markup, " ")
	markup = evidenceSVGPattern.ReplaceAllString(markup, " ")
	resolutionBase := base
	unsafeBase := false
	for _, element := range evidenceBasePattern.FindAllStringSubmatch(markup, maxEvidenceAnchors) {
		href, exists := htmlAttributes(element[1])["href"]
		if !exists {
			continue
		}
		reference, err := url.Parse(strings.TrimSpace(href))
		if err != nil {
			unsafeBase = true
		} else {
			candidate := base.ResolveReference(reference)
			if evidenceTargetAllowed(base, nil)(candidate) {
				resolutionBase = candidate
			} else {
				unsafeBase = true
			}
		}
		break // Only the first href base affects document link resolution.
	}
	seen := make(map[string]bool)
	var candidates []evidenceLink
	for _, match := range evidenceAnchorPattern.FindAllStringSubmatch(markup, maxEvidenceAnchors) {
		attributes := htmlAttributes(match[1])
		mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(attributes["type"], ";", 2)[0]))
		if mediaType != "" && mediaType != "text/html" && mediaType != "application/xhtml+xml" {
			continue
		}
		href := strings.TrimSpace(attributes["href"])
		if href == "" || strings.HasPrefix(href, "#") || len(href) > maxURLLength || strings.Contains(strings.ToLower(match[1]), "download") {
			continue
		}
		reference, err := url.Parse(href)
		if err != nil || (unsafeBase && !reference.IsAbs()) {
			continue
		}
		target := resolutionBase.ResolveReference(reference)
		target.Fragment = ""
		if !evidenceTargetAllowed(base, nil)(target) || !informationalEvidenceURL(target) || target.String() == base.String() {
			continue
		}
		label := strings.Join(strings.Fields(html.UnescapeString(allTagPattern.ReplaceAllString(match[2], " "))), " ")
		if label == "" || len([]rune(label)) > maxTitleLength || evidenceAction(label) {
			continue
		}
		if !privateEvent && privateEventLink(label) {
			continue
		}
		// Require the anchor itself to describe the page's purpose. An unrelated
		// link on a /menu parent does not become relevant by inheriting its path.
		score := evidenceLinkScore(label, menu, schedule, hours)
		if score == 0 {
			continue
		}
		// Prefer the explicitly requested service within the same menu-link
		// budget. Generic and other-service menus remain discovery fallbacks;
		// an anchor match does not prove the fetched page's applicability.
		if menu && len(requestedServices) > 0 {
			serviceLabel, matchingService := false, false
			for _, term := range queryTerms(label) {
				if service := menuServiceTerm(term); service != "" {
					serviceLabel = true
					matchingService = matchingService || requestedServices[service]
				}
			}
			if matchingService {
				score += 20
			} else if serviceLabel {
				// A generic Menu may include the requested service; a known
				// different service should not consume that scarce fetch first.
				score = 1
			}
		}
		if seen[target.String()] {
			continue
		}
		seen[target.String()] = true
		candidates = append(candidates, evidenceLink{url: target.String(), label: label, score: score})
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	if len(candidates) > maxEvidenceLinks {
		candidates = candidates[:maxEvidenceLinks]
	}
	return candidates
}

var evidenceFormPattern = regexp.MustCompile(`(?is)<form\b[^>]*>.*?</form\s*>`)

func evidenceLinkScore(label string, menu, schedule, hours bool) int {
	score := 0
	for _, term := range queryTerms(label) {
		if menu {
			switch term {
			case "menu", "menus":
				score += 10
			case "prices", "pricing":
				score += 5
			}
		}
		if schedule {
			switch term {
			case "schedule", "schedules", "showtimes", "showtime":
				score += 10
			case "calendar", "events":
				score += 5
			case "shows", "performances", "concerts", "comedy":
				score += 8
			}
		}
		if hours && (term == "hours" || term == "opening" || term == "closing") {
			score += 10
		}
	}
	return score
}

func menuServiceTerm(term string) string {
	switch term {
	case "breakfast", "brunch", "lunch", "dinner":
		return term
	case "dessert", "desserts":
		return "dessert"
	default:
		return ""
	}
}

func privateEventLink(label string) bool {
	for _, term := range queryTerms(label) {
		switch term {
		case "meeting", "meetings", "wedding", "weddings", "corporate", "conference", "conferences", "catering":
			return true
		}
	}
	return false
}

func evidenceAction(value string) bool {
	for _, term := range queryTerms(value) {
		switch term {
		case "order", "ordering", "checkout", "cart", "basket", "buy", "book", "booking", "reserve", "reservation", "reservations", "login", "logout", "signin", "signup", "register", "account", "subscribe", "unsubscribe", "delete", "remove", "cancel", "confirm", "payment", "pay", "purchase", "download":
			return true
		}
	}
	return false
}

func informationalEvidenceURL(target *url.URL) bool {
	if target == nil || target.RawQuery != "" || target.ForceQuery || len(target.String()) > maxURLLength || evidenceAction(target.Path) {
		return false
	}
	switch strings.ToLower(path.Ext(target.Path)) {
	case "", ".html", ".htm", ".php", ".aspx", ".asp":
		return true
	default:
		return false
	}
}

func evidenceTargetAllowed(base *url.URL, domains []string) func(*url.URL) bool {
	return func(target *url.URL) bool {
		if base == nil || target == nil || target.User != nil || target.Host == "" ||
			(target.Scheme != "http" && target.Scheme != "https") || !strings.EqualFold(base.Hostname(), target.Hostname()) {
			return false
		}
		// Same origin, except for the common public HTTP-to-HTTPS upgrade. No
		// subdomain expansion, alternate ports, or HTTPS downgrade.
		port := func(u *url.URL) string {
			if p := u.Port(); p != "" {
				return p
			}
			if u.Scheme == "https" {
				return "443"
			}
			return "80"
		}
		if base.Scheme == target.Scheme {
			if port(base) != port(target) {
				return false
			}
		} else if base.Scheme != "http" || target.Scheme != "https" || port(base) != "80" || port(target) != "443" {
			return false
		}
		host := strings.ToLower(strings.TrimSuffix(target.Hostname(), "."))
		return len(domains) == 0 || matchesAnyDomain(host, domains)
	}
}

func (e *pageExtractor) fetchEvidenceLinks(ctx context.Context, query string, parents []Result, links [][]evidenceLink, domains []string) []Result {
	seen := make(map[string]bool)
	for _, parent := range parents {
		if normalized, _, ok := normalizePublicResultURL(parent.URL); ok {
			seen[normalized] = true
		}
	}
	type task struct {
		link   evidenceLink
		parent Result
	}
	var tasks []task
	// Round-robin preserves breadth when multiple discovery results have links.
	for rank := 0; rank < maxEvidenceLinks && len(tasks) < maxLinkedPageFetches; rank++ {
		for index, candidates := range links {
			if rank >= len(candidates) || len(tasks) == maxLinkedPageFetches {
				continue
			}
			link := candidates[rank]
			if seen[link.url] {
				continue
			}
			seen[link.url] = true
			tasks = append(tasks, task{link: link, parent: parents[index]})
		}
	}
	results := make([]Result, len(tasks))
	var group sync.WaitGroup
	for index, task := range tasks {
		group.Add(1)
		go func(index int, link evidenceLink, parent Result) {
			defer group.Done()
			if ctx.Err() != nil {
				return
			}
			origin, err := url.Parse(parent.URL)
			if err != nil {
				return
			}
			allowed := evidenceTargetAllowed(origin, domains)
			page, err := e.fetchPageRestricted(ctx, link.url, func(target *url.URL) bool { return allowed(target) && informationalEvidenceURL(target) })
			if err != nil {
				return
			}
			selected := selectQueryChunks(query, page.text)
			pageExcerpts := completePageExcerpts(selected, maxSnippetLength)
			if len(pageExcerpts) == 0 {
				return
			}
			title := page.title
			if title == "" {
				title = link.label
			}
			results[index] = Result{Title: title, URL: page.sourceURL.String(),
				DiscoveredFrom: parent.URL, PageExcerpts: pageExcerpts, PagePublishedDate: page.publishedDate, ExtractionStatus: extractionSucceeded}
		}(index, task.link, task.parent)
	}
	group.Wait()
	var complete []Result
	// Deduplicate redirect destinations against parents and other children too.
	seen = make(map[string]bool)
	for _, parent := range parents {
		if normalized, _, ok := normalizePublicResultURL(parent.URL); ok {
			seen[normalized] = true
		}
	}
	for _, result := range results {
		if result.URL != "" && !seen[result.URL] {
			seen[result.URL] = true
			complete = append(complete, result)
		}
	}
	return complete
}
