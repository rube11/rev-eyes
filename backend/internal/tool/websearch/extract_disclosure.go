package websearch

import (
	"fmt"
	"html"
	"strings"

	xhtml "golang.org/x/net/html"
)

const (
	disclosureStartMarker = "[Disclosure start] "
	disclosureEndMarker   = "[Disclosure end]"
	maxDisclosureDepth    = 128
)

// Scope only the supported CSS checkbox/radio accordion structure. Its label
// belongs to its container, not all text until the next HTML heading. Original
// HTML remains available to link discovery; this rendering is extraction-only.
func preserveDisclosureSections(markup string) (string, map[string]string) {
	if !strings.Contains(markup, "accordion__tab") {
		return markup, nil
	}
	document, err := xhtml.Parse(strings.NewReader(markup))
	if err != nil {
		return markup, nil
	}
	var nodes []*xhtml.Node
	ids := make(map[string]int)
	var visit func(*xhtml.Node, int) bool
	visit = func(node *xhtml.Node, depth int) bool {
		if depth > maxDisclosureDepth {
			return false
		}
		nodes = append(nodes, node)
		if node.Type == xhtml.ElementNode {
			if id := disclosureAttribute(node, "id"); id != "" {
				ids[id]++
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if !visit(child, depth+1) {
				return false
			}
		}
		return true
	}
	if !visit(document, 0) {
		return markup, nil
	}
	// Literal page text cannot manufacture our new scope markers. These
	// temporary tokens are restored only after ordinary text was sanitized.
	prefix := "REV_EYES_DISCLOSURE_TOKEN_"
	decoded := html.UnescapeString(markup)
	for attempts := 0; strings.Contains(markup, prefix) || strings.Contains(decoded, prefix); attempts++ {
		if attempts == 8 {
			return markup, nil
		}
		prefix += "X"
	}
	markers := make(map[string]string)
	for _, node := range nodes {
		if node.Type == xhtml.CommentNode {
			if node.Parent != nil {
				node.Parent.RemoveChild(node)
			}
			continue
		}
		if !isDisclosureContainer(node) || disclosureInertAncestor(node) {
			continue
		}
		label, name := disclosureLabel(node, ids)
		if label == nil {
			continue
		}
		start := fmt.Sprintf("%s%d", prefix, len(markers))
		markers[start] = disclosureStartMarker + name
		end := fmt.Sprintf("%s%d", prefix, len(markers))
		markers[end] = disclosureEndMarker
		label.Parent.RemoveChild(label)
		node.InsertBefore(&xhtml.Node{Type: xhtml.TextNode, Data: "\n" + start + "\n"}, node.FirstChild)
		node.AppendChild(&xhtml.Node{Type: xhtml.TextNode, Data: "\n" + end + "\n"})
	}
	if len(markers) == 0 {
		return markup, nil
	}
	var rendered strings.Builder
	if err := xhtml.Render(&rendered, document); err != nil {
		return markup, nil
	}
	return rendered.String(), markers
}

func isDisclosureContainer(node *xhtml.Node) bool {
	return node.Type == xhtml.ElementNode && node.Data == "div" && disclosureClass(node, "accordion__tab")
}

func disclosureClass(node *xhtml.Node, class string) bool {
	for _, token := range strings.Fields(disclosureAttribute(node, "class")) {
		if token == class {
			return true
		}
	}
	return false
}

func disclosureAttribute(node *xhtml.Node, key string) string {
	value, found := "", false
	for _, attribute := range node.Attr {
		if attribute.Namespace == "" && attribute.Key == key {
			if found {
				return "" // Ambiguous duplicate bindings are not structural evidence.
			}
			value, found = attribute.Val, true
		}
	}
	return value
}

func disclosureInert(node *xhtml.Node) bool {
	if node.Type == xhtml.CommentNode {
		return true
	}
	if node.Type == xhtml.ElementNode {
		switch node.Data {
		case "script", "style", "noscript", "template", "textarea", "pre", "code", "nav", "footer", "select":
			return true
		}
	}
	return false
}

func disclosureInertAncestor(node *xhtml.Node) bool {
	for ancestor := node.Parent; ancestor != nil; ancestor = ancestor.Parent {
		if disclosureInert(ancestor) {
			return true
		}
	}
	return false
}

func disclosureLabel(container *xhtml.Node, ids map[string]int) (*xhtml.Node, string) {
	var labels []*xhtml.Node
	inputs := make(map[string][]*xhtml.Node)
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node != container && (isDisclosureContainer(node) || disclosureInert(node)) {
			return // Nested panels cannot lend this panel their control or label.
		}
		if node.Type == xhtml.ElementNode {
			if node.Data == "label" && disclosureClass(node, "accordion__tab-label") {
				labels = append(labels, node)
			}
			if node.Data == "input" {
				if id := disclosureAttribute(node, "id"); id != "" {
					inputs[id] = append(inputs[id], node)
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(container)
	if len(labels) != 1 {
		return nil, ""
	}
	label := labels[0]
	targetID := disclosureAttribute(label, "for")
	targets := inputs[targetID]
	if len(targets) != 1 || ids[targetID] != 1 {
		return nil, ""
	}
	typeName := strings.ToLower(disclosureAttribute(targets[0], "type"))
	if typeName != "checkbox" && typeName != "radio" {
		return nil, ""
	}
	var content strings.Builder
	var text func(*xhtml.Node)
	text = func(node *xhtml.Node) {
		if disclosureInert(node) {
			return
		}
		if node.Type == xhtml.TextNode {
			content.WriteString(node.Data)
			content.WriteByte(' ')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			text(child)
		}
	}
	text(label)
	name := strings.Join(strings.Fields(content.String()), " ")
	if name == "" || len([]rune(name)) > 240 {
		return nil, ""
	}
	return label, name
}
