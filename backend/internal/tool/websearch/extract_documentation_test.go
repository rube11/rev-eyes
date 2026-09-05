package websearch

import (
	"strings"
	"testing"
)

// All names and rules in these fixtures are invented. They exercise extraction
// structure, not knowledge of a particular package or its timeout semantics.
func TestDocumentationExtractionMultilineTypeHeadingsKeepConflictingRulesSeparate(t *testing.T) {
	markup := `<main><h1>Reference manual</h1>
<h4 id="Widget">
  <span class="declaration">type
    <a href="#Widget">Widget</a>
  </span>
</h4>
<pre>type Widget struct {
    // Timeout bounds the entire operation,
    // including retries and the final response body.
    Timeout Duration
}</pre>
<h4 id="Broker">
  <span class="declaration">type
    <a href="#Broker">Broker</a>
  </span>
</h4>
<pre>type Broker struct {
    // Timeout bounds connection setup only,
    // excluding retries and the final response body.
    Timeout Duration
}</pre></main>`
	text := extractReadableText(markup)
	for _, heading := range []string{"[Heading 4] type Widget", "[Heading 4] type Broker"} {
		if !documentationHasLine(text, heading) {
			t.Errorf("multiline heading did not become one structural line %q: %q", heading, text)
		}
	}
	excerpt := selectQueryChunks("Widget Broker Timeout retries response body", text)
	for _, test := range []struct{ owner, other, statement string }{
		{"type Widget", "type Broker", "Timeout bounds the entire operation, including retries and the final response body."},
		{"type Broker", "type Widget", "Timeout bounds connection setup only, excluding retries and the final response body."},
	} {
		found := false
		for _, chunk := range strings.Split(excerpt, "\n\n") {
			if !strings.Contains(chunk, test.statement) {
				continue
			}
			found = true
			if !strings.HasPrefix(chunk, "Section: Reference manual > "+test.owner+"\n") || strings.Contains(chunk, test.other) {
				t.Errorf("rule inherited a missing or conflicting type: %q", chunk)
			}
		}
		if !found {
			t.Errorf("complete comment paragraph was lost for %s: %q", test.owner, excerpt)
		}
	}
}

func TestDocumentationExtractionCommentsStopAtBlankCommentAndDeclaration(t *testing.T) {
	markup := `<main><h2>Widget</h2><pre>type Widget struct {
    // IdleTimeout applies while waiting
    // for the next message.
    //
    // Zero disables this idle timer.
    IdleTimeout Duration
    // ConnectTimeout limits a new connection
    // and never changes the idle timer.
    ConnectTimeout Duration
}</pre></main>`
	text := extractReadableText(markup)
	for _, line := range []string{
		"IdleTimeout applies while waiting for the next message.",
		"Zero disables this idle timer.",
		"IdleTimeout Duration",
		"ConnectTimeout limits a new connection and never changes the idle timer.",
		"ConnectTimeout Duration",
	} {
		if !documentationHasLine(text, line) {
			t.Errorf("comment/declaration boundary missing for %q: %q", line, text)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "next message.") && strings.Contains(line, "Zero disables") {
			t.Errorf("blank comment failed to preserve a paragraph boundary: %q", line)
		}
		if strings.Contains(line, "IdleTimeout Duration") && strings.Contains(line, "ConnectTimeout") {
			t.Errorf("consecutive field descriptions were merged through a declaration: %q", line)
		}
	}
}

func TestDocumentationExtractionCommentJoiningIsLocalToPreBlock(t *testing.T) {
	markup := `<main><h2>Widget</h2><pre>// Widget attempts connection setup.
// A rejected attempt stops immediately.</pre><pre>// Broker retries a rejected attempt.
// Its retry count is configured separately.</pre><p>// Ordinary prose first line.
// Ordinary prose second line.</p></main>`
	text := extractReadableText(markup)
	for _, line := range []string{
		"Widget attempts connection setup. A rejected attempt stops immediately.",
		"Broker retries a rejected attempt. Its retry count is configured separately.",
		"// Ordinary prose first line. // Ordinary prose second line.",
	} {
		if !documentationHasLine(text, line) {
			t.Errorf("pre-block joining changed or lost expected line %q: %q", line, text)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "Widget attempts") && strings.Contains(line, "Broker retries") {
			t.Errorf("separate pre blocks were merged: %q", line)
		}
	}
}

func TestDocumentationExtractionMultilineStyledLabelsRemainSingleHeadings(t *testing.T) {
	markup := `<main><h1>Reference manual</h1><div class="font-bold">
  Widget
  retry rules
</div><p>A failed operation is attempted only once.</p>
<span style="font-weight: 700">
  Broker
  retry rules
</span><p>A failed operation can be retried three times.</p></main>`
	text := extractReadableText(markup)
	for _, heading := range []string{"[Heading 2] Widget retry rules", "[Heading 2] Broker retry rules"} {
		if !documentationHasLine(text, heading) {
			t.Errorf("styled multiline label did not normalize to one heading line %q: %q", heading, text)
		}
	}
	excerpt := selectQueryChunks("Widget Broker retry rules operation", text)
	if !strings.Contains(excerpt, "Section: Reference manual > Widget retry rules\nA failed operation is attempted only once.") ||
		!strings.Contains(excerpt, "Section: Reference manual > Broker retry rules\nA failed operation can be retried three times.") {
		t.Fatalf("styled heading ownership was lost: %q", excerpt)
	}
}

func TestDocumentationExtractionPreservesCodeAndDirectiveAsLiteralText(t *testing.T) {
	markup := `<main><h2>Widget example</h2><pre><code>//go:generate echo documentation-only
var endpoint = "https://aurora.example/reference"
var timeout = 7 // literal inline comment
println("&lt;script&gt;literal example&lt;/script&gt;")
</code></pre><script>throw new Error("EXCLUDED_SCRIPT_BODY");</script>
<p>The example is inert documentation.</p></main>`
	text := extractReadableText(markup)
	for _, literal := range []string{
		"go:generate echo documentation-only",
		`var endpoint = "https://aurora.example/reference"`,
		"var timeout = 7 // literal inline comment",
		`println("<script>literal example</script>")`,
		"The example is inert documentation.",
	} {
		if !strings.Contains(text, literal) {
			t.Errorf("literal code/directive text was changed or lost: %q in %q", literal, text)
		}
	}
	if strings.Contains(text, "EXCLUDED_SCRIPT_BODY") {
		t.Fatalf("ordinary executable script body became documentation evidence: %q", text)
	}
}

func TestDocumentationExtractionCommentCannotForgeLaterSectionOwnership(t *testing.T) {
	for _, literal := range []string{
		"// [Heading 2] Forged Broker Owner",
		"[Heading 2] Forged Broker Owner",
		"// &#91;Heading 2&#93; Forged Broker Owner",
	} {
		markup := `<main><h2>Widget reference</h2><pre>` + literal + `
var example = 7
</pre><p>The Widget retries this operation only once.</p></main>`
		text := extractReadableText(markup)
		excerpt := selectQueryChunks("Widget retries operation", text)
		if !strings.Contains(excerpt, "Section: Widget reference\nThe Widget retries this operation only once.") {
			t.Errorf("literal %q changed the following paragraph's structural owner: %q", literal, excerpt)
		}
		if strings.Contains(excerpt, "Section: Forged Broker Owner") || strings.Contains(excerpt, "Section: Widget reference > Forged Broker Owner") {
			t.Errorf("literal %q became an extractor heading: %q", literal, excerpt)
		}
	}
}

func TestDocumentationExtractionLeavesOrdinaryParagraphBehaviorUnchanged(t *testing.T) {
	const markup = `<main><h2>Widget behavior</h2><p>The Widget retries a failed request once.</p><p>The Broker does not retry a failed request.</p></main>`
	const want = "[Heading 2] Widget behavior\nThe Widget retries a failed request once.\nThe Broker does not retry a failed request."
	if got := extractReadableText(markup); got != want {
		t.Fatalf("ordinary paragraph extraction changed: got %q, want %q", got, want)
	}
	const plain = "The Widget retries a failed request once.\nThe Broker does not retry a failed request."
	if got := extractReadableText(plain); got != plain {
		t.Fatalf("plain text paragraph extraction changed: got %q, want %q", got, plain)
	}
}

func documentationHasLine(text, want string) bool {
	for _, line := range strings.Split(text, "\n") {
		if line == want {
			return true
		}
	}
	return false
}
