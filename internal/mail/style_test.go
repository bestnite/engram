package mail

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// TestStyleFragmentButtonRule pins down the one structural exception in the style pass: a paragraph
// whose only content is a link becomes a button, and every other link stays an inline link. The cases
// are the ones the rule deliberately excludes -- two links, a link among other text, a link wrapped in
// emphasis, and an empty href.
func TestStyleFragmentButtonRule(t *testing.T) {
	cases := []struct {
		name       string
		fragment   string
		wantButton bool
	}{
		{"sole link", `<p><a href="https://example.com">Go</a></p>`, true},
		{"sole link with surrounding whitespace", "<p>\n  <a href=\"https://example.com\">Go</a>\n</p>", true},
		{"link among text", `<p>Read <a href="https://example.com">this</a> first.</p>`, false},
		{"two links", `<p><a href="https://example.com/a">A</a><a href="https://example.com/b">B</a></p>`, false},
		{"link wrapped in strong", `<p><strong><a href="https://example.com">Go</a></strong></p>`, false},
		{"empty href", `<p><a href="">Go</a></p>`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			htmlOut, _, err := styleFragment(tc.fragment)
			if err != nil {
				t.Fatalf("styleFragment() error = %v", err)
			}
			hasButton := strings.Contains(htmlOut, buttonStyle)
			if hasButton != tc.wantButton {
				t.Fatalf("button style present = %v, want %v:\n%s", hasButton, tc.wantButton, htmlOut)
			}
			if !tc.wantButton && !strings.Contains(htmlOut, bodyStyle["a"]) {
				t.Errorf("inline link style missing:\n%s", htmlOut)
			}
		})
	}
}

// TestStyleFragmentAppliesBodyStyles asserts that the style map reaches every tag the mail body may
// contain, including headings, so the designed mail does not fall back to browser defaults.
func TestStyleFragmentAppliesBodyStyles(t *testing.T) {
	fragment := `<h1>a</h1><h2>b</h2><h3>c</h3><p>d</p><hr>` +
		`<blockquote><p>e</p></blockquote>` +
		`<ul><li>f</li></ul><ol><li>g</li></ol>` +
		`<pre><code>h</code></pre>` +
		`<table><tr><th>i</th><td>j</td></tr></table>`
	htmlOut, _, err := styleFragment(fragment)
	if err != nil {
		t.Fatalf("styleFragment() error = %v", err)
	}
	// The serializer escapes quotes inside attribute values as entities, which is the same value once
	// parsed; comparing the unescaped text keeps the assertion about the style itself.
	unescaped := html.UnescapeString(htmlOut)
	for tag, style := range bodyStyle {
		if !strings.Contains(fragment, "<"+tag) {
			continue
		}
		if !strings.Contains(unescaped, style) {
			t.Errorf("element %q did not receive its style:\n%s", tag, htmlOut)
		}
	}
}

// TestStyleFragmentDoesNotOverwriteExistingStyle documents the guard: the sanitiser removes every
// style attribute, so the guard is about future allowlist changes rather than today's input.
func TestStyleFragmentDoesNotOverwriteExistingStyle(t *testing.T) {
	htmlOut, _, err := styleFragment(`<p style="color:red">x</p>`)
	if err != nil {
		t.Fatalf("styleFragment() error = %v", err)
	}
	if !strings.Contains(htmlOut, `style="color:red"`) {
		t.Errorf("existing style was overwritten:\n%s", htmlOut)
	}
	if strings.Contains(htmlOut, bodyStyle["p"]) {
		t.Errorf("body style was added on top of an existing style:\n%s", htmlOut)
	}
}

// TestStyleFragmentPlainText pins down the plain-text derivation: blocks are separated by a blank
// line, list items are prefixed, links carry their target, images keep their alt text, horizontal
// rules are drawn as a line, and indentation from the HTML source does not leak into the text.
func TestStyleFragmentPlainText(t *testing.T) {
	fragment := `<h2>Title</h2>` +
		`<p>Some <strong>bold</strong> and <em>italic</em> text<br>after a break.</p>` +
		`<ul><li>One</li><li>Two</li></ul>` +
		`<blockquote><p>A note.</p></blockquote>` +
		`<p><a href="https://example.com/x">Label</a> and <a href="https://example.com/y">https://example.com/y</a></p>` +
		`<hr>` +
		`<p><img src="/x" alt="Alt text"></p>` +
		`<p>Do not collapse <code>a  b</code>.</p>`
	_, plain, err := styleFragment(fragment)
	if err != nil {
		t.Fatalf("styleFragment() error = %v", err)
	}
	want := "Title\n\n" +
		"Some bold and italic text\nafter a break.\n\n" +
		"- One\n- Two\n\n" +
		"A note.\n\n" +
		"Label (https://example.com/x) and https://example.com/y\n\n" +
		"----------\n\n" +
		"[Alt text]\n\n" +
		"Do not collapse a  b."
	if plain != want {
		t.Errorf("plain text =\n%q\nwant\n%q", plain, want)
	}
	if strings.Contains(plain, "\n\n\n") {
		t.Errorf("plain text = %q, want at most one blank line between blocks", plain)
	}
}

// TestStyleFragmentPlainTextEmptyLinkText asserts that a link whose text is empty keeps its target,
// so the destination is not silently lost from the text-only mail.
func TestStyleFragmentPlainTextEmptyLinkText(t *testing.T) {
	_, plain, err := styleFragment(`<p><a href="https://example.com/only"></a></p>`)
	if err != nil {
		t.Fatalf("styleFragment() error = %v", err)
	}
	if plain != "https://example.com/only" {
		t.Errorf("plain text = %q, want the bare URL", plain)
	}
}

// TestStyleFragmentKeepsTextDecoded asserts that entities are decoded in the text-only part: the
// HTML fragment carries escaped entities, but a text reader must see the characters they stand for.
func TestStyleFragmentKeepsTextDecoded(t *testing.T) {
	_, plain, err := styleFragment(`<p>a &lt; b &amp; c &#34;quoted&#34;</p>`)
	if err != nil {
		t.Fatalf("styleFragment() error = %v", err)
	}
	if plain != `a < b & c "quoted"` {
		t.Errorf("plain text = %q, want entities decoded", plain)
	}
}
