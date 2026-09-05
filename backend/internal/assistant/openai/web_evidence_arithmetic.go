package openai

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const subtotalQuantity = `(?:[1-9][0-9]?|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|sixteen|seventeen|eighteen|nineteen|twenty)`

var (
	subtotalParty             = regexp.MustCompile(`(?i)\b(` + subtotalQuantity + `)\s+(?:people|persons|adults|children|diners|guests)\b`)
	subtotalAmbiguousQuantity = regexp.MustCompile(`(?i)\b` + subtotalQuantity + `\s*(?:or|and|to|[-–—/])\s*` + subtotalQuantity + `\b`)
	subtotalUncertainParty    = regexp.MustCompile(`(?i)\b(?:up\s+to|at\s+least|at\s+most|about|approximately|around|more\s+than|less\s+than|over|under)\s+` + subtotalQuantity + `\s+(?:people|persons|adults|children|diners|guests)\b`)
	subtotalPrice             = regexp.MustCompile(`\$\s*([0-9][0-9.,]*)`)
	subtotalPriceRange        = regexp.MustCompile(`(?i)\$\s*[0-9][0-9.,]*\s*(?:[-–—/]|to|through)\s*\$?\s*[0-9]`)
	subtotalBeforeTax         = regexp.MustCompile(`(?i)(\$\s*([0-9][0-9.,]*))\s+before\s+tax\s+and\s+tip\b`)
	subtotalOtherCurrency     = regexp.MustCompile(`(?i)[€£¥₹]|\b(?:CAD|AUD|NZD|HKD|SGD|EUR|GBP|JPY|CNY|INR)\b|\b(?:CA|AU|NZ|HK|SG|C|A)\s*\$`)
	subtotalNonExact          = regexp.MustCompile(`(?i)(?:[-−]\s*\$|\$\s*[-−])|\b(?:from|starting\s+at|up\s+to|about|approximately)\s+\$`)
	subtotalPriceSuffix       = regexp.MustCompile(`(?i)^\s*(?:and\s+up|or\s+more|plus|minimum)\b`)
	subtotalTaxContradiction  = regexp.MustCompile(`(?i)\ball[\s-]?in\b|\b(?:includes?|including|with)\s+(?:tax|tip)\b|\b(?:tax|tip)(?:\s+and\s+(?:tax|tip))?\s+included\b`)
)

// claimNumbersSupportedForQuery allows only an exact unit-price times party-size
// subtotal explicitly excluding tax and tip. This is arithmetic validation, not
// proof of portion adequacy, item ownership, diet, or branch applicability.
// quotes must be the claim's support quote, never aggregated temporal fields.
func claimNumbersSupportedForQuery(text, quotes, query string, location *time.Location) bool {
	if claimNumbersSupported(text, quotes, location) {
		return true
	}
	if subtotalOtherCurrency.MatchString(text+" "+quotes) || subtotalPriceRange.MatchString(quotes) ||
		subtotalNonExact.MatchString(quotes) || subtotalTaxContradiction.MatchString(text) {
		return false
	}
	quantity, claimSpan, ok := explicitSubtotalParty(text)
	requested, _, requestOK := explicitSubtotalParty(query)
	if !ok || !requestOK || quantity != requested {
		return false
	}
	var unit int64
	prices := subtotalPrice.FindAllStringSubmatchIndex(quotes, -1)
	if len(prices) == 0 {
		return false
	}
	for _, price := range prices {
		tail := quotes[price[1]:]
		if tail != "" {
			next, _ := utf8.DecodeRuneInString(tail)
			if next == '+' || next == '−' || unicode.IsLetter(next) || unicode.IsDigit(next) || subtotalPriceSuffix.MatchString(tail) {
				return false
			}
		}
		cents, valid := subtotalCents(strings.TrimSuffix(quotes[price[2]:price[3]], "."))
		if !valid || cents == 0 || unit != 0 && cents != unit {
			return false
		}
		unit = cents
	}
	subtotals := subtotalBeforeTax.FindAllStringSubmatchIndex(text, -1)
	if len(subtotals) != 1 {
		return false
	}
	subtotal := subtotals[0]
	// The amount must be a positive subtotal, not a signed adjustment whose
	// sign would be left behind when the independently checked number is masked.
	prefix := strings.TrimSpace(text[:subtotal[2]])
	if strings.HasSuffix(prefix, "-") || strings.HasSuffix(prefix, "−") || strings.HasSuffix(prefix, "+") {
		return false
	}
	total, valid := subtotalCents(text[subtotal[4]:subtotal[5]])
	if !valid || unit*int64(quantity) != total {
		return false
	}
	// Exempt only the independently checked amount and the query-matched party
	// count. Do not append derived numbers or the user's budget to source facts.
	masked := []byte(text)
	for _, span := range [][2]int{{subtotal[2], subtotal[3]}, claimSpan} {
		for i := span[0]; i < span[1]; i++ {
			masked[i] = ' '
		}
	}
	return claimNumbersSupported(string(masked), quotes, location)
}

func explicitSubtotalParty(text string) (int, [2]int, bool) {
	matches := subtotalParty.FindAllStringSubmatchIndex(text, -1)
	if len(matches) != 1 || subtotalAmbiguousQuantity.MatchString(text) || subtotalUncertainParty.MatchString(text) {
		return 0, [2]int{}, false
	}
	span := [2]int{matches[0][2], matches[0][3]}
	if span[0] > 0 {
		prefix := strings.TrimRight(text[:span[0]], " \t")
		previous, _ := utf8.DecodeLastRuneInString(prefix)
		if strings.ContainsRune(".-−+", previous) || unicode.IsDigit(previous) {
			return 0, [2]int{}, false
		}
	}
	word := strings.ToLower(text[span[0]:span[1]])
	count, _ := strconv.Atoi(word)
	if count == 0 {
		for index, name := range strings.Fields("one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen nineteen twenty") {
			if name == word {
				count = index + 1
				break
			}
		}
	}
	return count, span, count >= 1 && count <= 20
}

// Bounded integer cents avoids rounding allowances and multiplication overflow.
// Commas, signs, scientific notation and fractions finer than a cent fail closed.
func subtotalCents(value string) (int64, bool) {
	parts := strings.Split(value, ".")
	if len(parts) > 2 || len(parts[0]) == 0 || len(parts[0]) > 6 {
		return 0, false
	}
	for _, r := range value {
		if r != '.' && (r < '0' || r > '9') {
			return 0, false
		}
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, false
	}
	var fraction int64
	if len(parts) == 2 {
		if len(parts[1]) < 1 || len(parts[1]) > 2 {
			return 0, false
		}
		fraction, _ = strconv.ParseInt(parts[1], 10, 64)
		if len(parts[1]) == 1 {
			fraction *= 10
		}
	}
	return whole*100 + fraction, true
}
