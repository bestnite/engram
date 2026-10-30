package mailstyle

import (
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"
)

// TestStyleFragmentButtonRule pins down the one structural rule: a paragraph whose only content is
// one non-empty link becomes a button, and every other link keeps the inline link style. The cases
// are the ones the rule deliberately excludes.
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
			htmlOut, err := StyleFragment(tc.fragment)
			if err != nil {
				t.Fatalf("StyleFragment() error = %v", err)
			}
			hasButton := strings.Contains(htmlOut, Button)
			if hasButton != tc.wantButton {
				t.Fatalf("button style present = %v, want %v:\n%s", hasButton, tc.wantButton, htmlOut)
			}
			if !tc.wantButton && !strings.Contains(htmlOut, Body["a"]) {
				t.Errorf("inline link style missing:\n%s", htmlOut)
			}
		})
	}
}

// TestStyleFragmentAppliesBodyStyles asserts that the style map reaches every tag a mail body may
// contain, including headings, so a converted template does not fall back to browser defaults.
func TestStyleFragmentAppliesBodyStyles(t *testing.T) {
	fragment := `<h1>a</h1><h2>b</h2><h3>c</h3><p>d</p><hr>` +
		`<blockquote><p>e</p></blockquote>` +
		`<ul><li>f</li></ul><ol><li>g</li></ol>` +
		`<pre><code>h</code></pre>` +
		`<table><tr><th>i</th><td>j</td></tr></table>`
	htmlOut, err := StyleFragment(fragment)
	if err != nil {
		t.Fatalf("StyleFragment() error = %v", err)
	}
	// The serialiser escapes quotes inside attribute values as entities, which is the same value once
	// parsed; comparing the unescaped text keeps the assertion about the style itself.
	unescaped := xhtml.UnescapeString(htmlOut)
	for tag, style := range Body {
		if !strings.Contains(fragment, "<"+tag) {
			continue
		}
		if !strings.Contains(unescaped, style) {
			t.Errorf("element %q did not receive its style:\n%s", tag, htmlOut)
		}
	}
}

// TestStyleFragmentDoesNotOverwriteExistingStyle documents the guard: an element that already carries
// a style keeps it, so arbitrary CSS an administrator writes survives the conversion.
func TestStyleFragmentDoesNotOverwriteExistingStyle(t *testing.T) {
	htmlOut, err := StyleFragment(`<p style="color:red">x</p>`)
	if err != nil {
		t.Fatalf("StyleFragment() error = %v", err)
	}
	if !strings.Contains(htmlOut, `style="color:red"`) {
		t.Errorf("existing style was overwritten:\n%s", htmlOut)
	}
	if strings.Contains(htmlOut, Body["p"]) {
		t.Errorf("body style was added on top of an existing style:\n%s", htmlOut)
	}
}

// TestMarkdownToHTMLConvertsLegacyBody covers the migration's conversion: a stored Markdown body
// becomes the same inline-styled HTML the old renderer produced.
func TestMarkdownToHTMLConvertsLegacyBody(t *testing.T) {
	got, err := MarkdownToHTML("## Title\n\nHello **world**.\n\n[Go]({{url}})")
	if err != nil {
		t.Fatalf("MarkdownToHTML() error = %v", err)
	}
	for _, want := range []string{
		`<h2 style="` + Body["h2"] + `">Title</h2>`,
		`<strong style="` + Body["strong"] + `">world</strong>`,
		`<a href="{{url}}" style="` + Button + `">Go</a>`,
	} {
		if !strings.Contains(xhtml.UnescapeString(got), want) {
			t.Errorf("converted HTML does not contain %q:\n%s", want, got)
		}
	}
}

// TestMarkdownToHTMLDegradesShortcodes keeps the old visible-text rule: a mail has no client-side
// controls, so a shortcode contributes only its text.
func TestMarkdownToHTMLDegradesShortcodes(t *testing.T) {
	got, err := MarkdownToHTML("{{tts:spoken text|voice}}\n\n{{ruby:base|reading}}")
	if err != nil {
		t.Fatalf("MarkdownToHTML() error = %v", err)
	}
	if !strings.Contains(got, "spoken text") || !strings.Contains(got, "base(reading)") {
		t.Errorf("shortcode visible text missing:\n%s", got)
	}
	if strings.Contains(got, "{{tts") || strings.Contains(got, "{{ruby") {
		t.Errorf("shortcode marker survived:\n%s", got)
	}
}
