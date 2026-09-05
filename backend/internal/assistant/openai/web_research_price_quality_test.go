package openai

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/rube11/rev-eyes/backend/internal/tool/websearch"
)

var (
	qualityDollarExpression    = regexp.MustCompile(`\$\s*([0-9]+(?:,[0-9]{3})*(?:\.[0-9]{1,2})?)(?:\s*(?:-|–|—|to)\s*\$?\s*([0-9]+(?:,[0-9]{3})*(?:\.[0-9]{1,2})?))?`)
	qualityQuotedPriceLead     = regexp.MustCompile(`(?i)\b(?:from|starting\s+(?:at|from)|starts?\s+(?:at|from)|priced?\s+at|listed\s+at|about|around|approximately)\s*~?\s*$`)
	qualityPriceListing        = regexp.MustCompile(`(?i)\b(?:guide|menu|site|page|source|listing)\s+(?:lists?|quotes?|shows?|says|has)\b[^.!?;\n]{0,80}$`)
	qualityPriceInference      = regexp.MustCompile(`(?i)\b(?:estimated?|calculated?|arithmetic|inferred|assuming|if\s+you|would\s+(?:be|total|cost)|works?\s+out\s+to|adds?\s+up\s+to)\b`)
	qualityPriceBudget         = regexp.MustCompile(`(?i)\b(?:budget|target|limit|cap|ceiling|aim)\s*(?:(?:is|of|at|near|for\s+(?:two|2))\s*)?$`)
	qualityPriceBudgetSuffix   = regexp.MustCompile(`(?i)^\s*(?:budget|target|limit|cap|ceiling)\b`)
	qualityPriceClauseBoundary = regexp.MustCompile(`[;!?\n]|\.\s+`)
)

type qualityPriceExpression struct {
	text   string
	start  int
	end    int
	amount []int64
}

// This is deliberately a narrow contradiction/provenance screen, not a price
// entailment grader. It recognizes the glasses format's adjacent citations and
// explicit quoted/listed price wording, not arbitrary dollar values or budgets.
// Publisher-only citations can identify several retrieved pages from that host;
// exact URLs are restricted to the cited page. Even a successful numeric match
// still requires a person to verify the venue, price meaning and current date.
func qualityQuotedPriceCitationCheck(response string, sources map[string]websearch.Result) liveQualityCheck {
	checked := 0
	violations := make([]string, 0)
	checkClaim := func(claim, citation string) {
		matched := qualityCitedPriceSources(citation, sources)
		if len(matched) == 0 {
			return // The existing citation_provenance gate handles absent/ambiguous identities.
		}
		for _, price := range qualityPriceExpressions(claim) {
			if !qualityIsQuotedPrice(claim, price) {
				continue
			}
			checked++
			found := false
			for _, canonical := range matched {
				source := sources[canonical]
				for _, block := range qualityEvidenceBlocks(source) {
					for _, evidence := range qualityPriceExpressions(block) {
						if qualityPriceMatches(price, evidence) {
							found = true
							break
						}
					}
				}
				if found {
					break
				}
			}
			if !found {
				violations = append(violations, fmt.Sprintf("%s absent from %s", price.text, strings.TrimSpace(citation)))
			}
		}
	}
	for _, line := range strings.Split(response, "\n") {
		start := 0
		hasCitation := false
		for _, span := range qualityParentheticalSource.FindAllStringIndex(line, -1) {
			citation := line[span[0]:span[1]]
			if len(qualityCitedPriceSources(citation, sources)) == 0 {
				continue // A numeric/caveat parenthetical is not a claim boundary.
			}
			checkClaim(line[start:span[0]], citation)
			start = span[1]
			hasCitation = true
		}
		if !hasCitation {
			// Also support a single explicit Source: label or inline publisher
			// hostname on a line. Multiple distinct publishers on that line are
			// ambiguous attachment; leave them to manual review, never pool them.
			identities := qualityResponseSourceNames(line)
			urls := qualityResponseURLs(line)
			if len(identities)+len(urls) == 1 {
				checkClaim(line, line)
			}
		}
	}
	return liveQualityCheck{
		Name: "quoted_price_citation_support", Passed: len(violations) == 0,
		Detail: fmt.Sprintf("%d explicit quoted/listed price expressions screened against their adjacent cited source; %d absent: %s. Budgets and labeled calculations are excluded; numeric presence is not claim entailment.", checked, len(violations), strings.Join(violations, "; ")),
	}
}

func qualityCitedPriceSources(citation string, sources map[string]websearch.Result) []string {
	matched := make([]string, 0)
	seen := make(map[string]bool)
	add := func(canonical string) {
		if _, ok := sources[canonical]; ok && !seen[canonical] {
			seen[canonical] = true
			matched = append(matched, canonical)
		}
	}
	urls := qualityResponseURLs(citation)
	if len(urls) > 0 {
		for _, canonical := range urls {
			add(canonical)
		}
		return matched // Do not broaden an exact URL to other pages on its host.
	}
	for _, name := range qualityResponseSourceNames(citation) {
		for _, canonical := range qualityResolveSourceName(name, sources) {
			add(canonical)
		}
	}
	return matched
}

func qualityPriceExpressions(text string) []qualityPriceExpression {
	prices := make([]qualityPriceExpression, 0)
	for _, span := range qualityDollarExpression.FindAllStringSubmatchIndex(text, -1) {
		price := qualityPriceExpression{text: text[span[0]:span[1]], start: span[0], end: span[1]}
		for _, group := range []int{2, 4} {
			if span[group] < 0 {
				continue
			}
			raw := strings.ReplaceAll(text[span[group]:span[group+1]], ",", "")
			parts := strings.SplitN(raw, ".", 2)
			fraction := "00"
			if len(parts) == 2 {
				fraction = (parts[1] + "00")[:2]
			}
			cents, err := strconv.ParseInt(parts[0]+fraction, 10, 64)
			if err == nil {
				price.amount = append(price.amount, cents)
			}
		}
		if len(price.amount) > 0 {
			prices = append(prices, price)
		}
	}
	return prices
}

func qualityIsQuotedPrice(claim string, price qualityPriceExpression) bool {
	prefix := claim[:price.start]
	// Apply exceptions to the current clause, not another recommendation or
	// a global disclaimer. "Availability isn't verified" never excuses a
	// positively quoted price that is absent from the attributed publisher.
	if boundaries := qualityPriceClauseBoundary.FindAllStringIndex(prefix, -1); len(boundaries) > 0 {
		prefix = prefix[boundaries[len(boundaries)-1][1]:]
	}
	listing := qualityPriceListing.MatchString(prefix)
	lead := qualityQuotedPriceLead.FindStringIndex(prefix)
	if !listing && lead == nil {
		return false
	}
	if !listing && (qualityPriceBudget.MatchString(prefix[:lead[0]]) || qualityPriceBudgetSuffix.MatchString(claim[price.end:]) || qualityPriceInference.MatchString(prefix)) {
		return false
	}
	return true
}

func qualityPriceMatches(claim, source qualityPriceExpression) bool {
	if len(claim.amount) == 1 {
		// A displayed range includes its lower/from price. A scalar match is
		// only a numeric signal, not proof of the amount's real-world meaning.
		return claim.amount[0] == source.amount[0]
	}
	return len(source.amount) == 2 && claim.amount[0] == source.amount[0] && claim.amount[1] == source.amount[1]
}
