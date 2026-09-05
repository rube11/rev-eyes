package websearch

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
)

// This is deliberately narrower than general numeric text: the complete value
// must come from a supported card's explicit item-price span. Preserve the
// literal currency, if supplied; a bare amount does not acquire one here.
var menuCardPricePattern = regexp.MustCompile(`^(?:(?:[$€£]\s*|(?:USD|EUR|GBP|CAD|AUD)\s+)?[0-9]{1,6}(?:\.[0-9]{2})?|[0-9]{1,6}(?:\.[0-9]{2})?\s+(?:USD|EUR|GBP|CAD|AUD))$`)

// Preserve the supported single-price DOM card before ordinary heading/P/DIV
// stripping separates its dish, price, and late restrictions. This is not a
// generic menu classifier: variant, nested, or otherwise ambiguous cards are
// discarded whole rather than emitting a price without its owner/qualifiers.
func preserveMenuItemCards(markup string) string {
	if !strings.Contains(markup, "menu-item") {
		return markup
	}
	document, err := xhtml.Parse(strings.NewReader(markup))
	if err != nil {
		return markup
	}
	changed := false
	var visit func(*xhtml.Node, int)
	visit = func(node *xhtml.Node, depth int) {
		if depth > maxDisclosureDepth {
			return
		}
		if node.Type == xhtml.ElementNode && node.Data == "section" && disclosureClass(node, "menu-item") {
			label := ""
			var heading *xhtml.Node
			if !menuCardInertAncestor(node) {
				label, heading = ownedMenuCardRecord(node)
			}
			if heading != nil {
				// Clear an earlier sibling heading (for example Dressings) at
				// the card's level. The atomic record owns its name; keeping it
				// active as a heading would mislabel prose after the closed card.
				boundary := &xhtml.Node{Type: xhtml.ElementNode, Data: heading.Data, DataAtom: heading.DataAtom}
				node.Parent.InsertBefore(boundary, node)
			}
			node.Parent.InsertBefore(&xhtml.Node{Type: xhtml.TextNode, Data: "\n" + label + "\n"}, node)
			node.Parent.RemoveChild(node)
			changed = true
			return
		}
		for child := node.FirstChild; child != nil; {
			next := child.NextSibling
			visit(child, depth+1)
			child = next
		}
	}
	visit(document, 0)
	if !changed {
		return markup
	}
	var rendered strings.Builder
	if err := xhtml.Render(&rendered, document); err != nil {
		return markup
	}
	return rendered.String()
}

func ownedMenuCardRecord(card *xhtml.Node) (string, *xhtml.Node) {
	var heading, price *xhtml.Node
	valid := true
	var inspect func(*xhtml.Node, int)
	inspect = func(node *xhtml.Node, depth int) {
		if !valid || depth > maxDisclosureDepth {
			valid = false
			return
		}
		if menuCardInert(node) {
			return
		}
		if node.Type == xhtml.ElementNode {
			if node != card && (disclosureClass(node, "menu-item") || isDisclosureContainer(node)) {
				valid = false // Nested item/variant scopes are not one owned record.
				return
			}
			if len(node.Data) == 2 && node.Data[0] == 'h' && node.Data[1] >= '1' && node.Data[1] <= '6' {
				if heading != nil {
					valid = false
					return
				}
				heading = node
			}
			if node.Data == "span" && disclosureClass(node, "item-price") {
				if price != nil || disclosureClass(node, "item-addon") || menuCardAddonAncestor(node, card) {
					valid = false // Multiple/variant amounts or an add-on are not one base price.
					return
				}
				price = node
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			inspect(child, depth+1)
		}
	}
	inspect(card, 0)
	if !valid || heading == nil || price == nil {
		return "", nil
	}
	name, amount := menuCardText(heading, nil, nil), menuCardText(price, nil, nil)
	letters := 0
	for _, character := range name {
		if unicode.IsLetter(character) {
			letters++
		}
	}
	if letters < 2 || !menuCardPricePattern.MatchString(amount) {
		return "", nil
	}
	// All remaining visible text is retained, not just known description/add-on
	// classes: a small or otherwise unlabeled child may carry a restriction.
	// Empty icon classes are not visible diet/allergy assertions and add no text.
	label := menuItemPrefix + name + "; item-price=" + amount
	if remaining := menuCardText(card, heading, price); remaining != "" {
		label += "; " + remaining
	}
	if utf8.RuneCountInString(label) > maxChunkLength {
		return "", nil
	}
	return label, heading
}

func menuCardText(node, omitHeading, omitPrice *xhtml.Node) string {
	var content strings.Builder
	var visit func(*xhtml.Node)
	visit = func(current *xhtml.Node) {
		if current == omitHeading || current == omitPrice || menuCardInert(current) {
			return
		}
		if current.Type == xhtml.TextNode {
			content.WriteString(current.Data)
			content.WriteByte(' ')
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(node)
	return strings.Join(strings.Fields(content.String()), " ")
}

func menuCardAddonAncestor(node, card *xhtml.Node) bool {
	for ancestor := node.Parent; ancestor != nil && ancestor != card; ancestor = ancestor.Parent {
		if disclosureClass(ancestor, "item-addon") {
			return true
		}
	}
	return false
}

func menuCardInert(node *xhtml.Node) bool {
	if disclosureInert(node) || (node.Type == xhtml.ElementNode && node.Data == "svg") {
		return true
	}
	if node.Type == xhtml.ElementNode {
		for _, attribute := range node.Attr {
			if attribute.Namespace == "" && attribute.Key == "hidden" {
				return true
			}
		}
	}
	return false
}

func menuCardInertAncestor(node *xhtml.Node) bool {
	for ancestor := node.Parent; ancestor != nil; ancestor = ancestor.Parent {
		if menuCardInert(ancestor) {
			return true
		}
	}
	return false
}
