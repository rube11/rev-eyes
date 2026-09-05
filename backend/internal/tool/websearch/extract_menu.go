package websearch

import (
	"html"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const menuItemPrefix = "Menu item: "

var menuServicePeriodPattern = regexp.MustCompile(`(?i)^\d{1,2}(?::\d{2})?\s*[ap]\.?m\.?\s*(?:-|–|—|to)\s*\d{1,2}(?::\d{2})?\s*[ap]\.?m\.?$`)
var menuNestedAnchorPattern = regexp.MustCompile(`(?i)<a(?:\s|>)`)

// A link to an explicitly identified menu item supplies a bounded ownership
// container. Flatten its name, price, description and qualifiers together before
// ordinary DIV/P stripping can separate them. Never follow the item/order URL.
func preserveMenuItemLinks(markup string) string {
	return evidenceAnchorPattern.ReplaceAllStringFunc(markup, func(element string) string {
		match := evidenceAnchorPattern.FindStringSubmatch(element)
		attributes := htmlAttributes(match[1])
		if !isMenuItemLink(attributes) {
			return element
		}
		if menuNestedAnchorPattern.MatchString(match[2]) {
			return " " // Malformed/nested item links are not one ownership container.
		}
		label := strings.Join(strings.Fields(html.UnescapeString(allTagPattern.ReplaceAllString(match[2], " "))), " ")
		price := priceEvidencePattern.FindStringIndex(label)
		if price == nil {
			return element
		}
		letters := 0
		for _, char := range label[:price[0]] {
			if unicode.IsLetter(char) {
				letters++
			}
		}
		// A price alone is not an item identity. Oversized records are dropped
		// whole: a late allergy, availability or service qualifier must not be
		// removed while the price remains apparently unconditional.
		if letters < 2 || utf8.RuneCountInString(menuItemPrefix+label) > maxChunkLength {
			return " "
		}
		return "\n" + html.EscapeString(menuItemPrefix+label) + "\n"
	})
}

func isMenuItemLink(attributes map[string]string) bool {
	if strings.EqualFold(attributes["itemtype"], "https://schema.org/MenuItem") ||
		strings.EqualFold(attributes["itemtype"], "http://schema.org/MenuItem") {
		return true
	}
	target, err := url.Parse(strings.TrimSpace(attributes["href"]))
	if err != nil || target == nil || target.User != nil ||
		(target.Scheme != "" && target.Scheme != "http" && target.Scheme != "https") {
		return false
	}
	parts := strings.Split(strings.Trim(strings.ToLower(target.Path), "/"), "/")
	for index, part := range parts {
		if part != "menu" && part != "menus" {
			continue
		}
		if strings.TrimSpace(target.Query().Get("item")) != "" || strings.TrimSpace(target.Query().Get("item_id")) != "" {
			return true
		}
		if index+2 < len(parts) && (parts[index+1] == "item" || parts[index+1] == "items") && parts[index+2] != "" {
			return true
		}
	}
	return false
}
