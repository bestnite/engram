package render

import (
	"strings"
	"testing"
)

// mailHeadingElements are the only elements the mail policy adds to the card allowlist.
var mailHeadingElements = map[string]bool{"h1": true, "h2": true, "h3": true}

// TestRenderMailMarkdownAllowsHeadings is the reason the mail entry point exists: a mail body has a
// title, and the card policy strips headings, so `## Title` would degrade into plain text and the
// mail's title style would never match.
func TestRenderMailMarkdownAllowsHeadings(t *testing.T) {
	out, err := RenderMailMarkdown("## Title\n\n### Section\n\n# Page")
	if err != nil {
		t.Fatalf("RenderMailMarkdown() error = %v", err)
	}
	for _, want := range []string{"<h2>Title</h2>", "<h3>Section</h3>", "<h1>Page</h1>"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lost heading %q:\n%s", want, out)
		}
	}
}

// TestRenderMarkdownStillStripsHeadings asserts that the card policy did not gain headings while the
// mail policy did: card content must behave exactly as before.
func TestRenderMarkdownStillStripsHeadings(t *testing.T) {
	out, err := RenderMarkdown("## Title")
	if err != nil {
		t.Fatalf("RenderMarkdown() error = %v", err)
	}
	if strings.Contains(out, "<h2") {
		t.Errorf("card content kept a heading:\n%s", out)
	}
	if !strings.Contains(out, "Title") {
		t.Errorf("card content lost the heading text:\n%s", out)
	}
}

// TestRenderMailMarkdownKeepsTheCardAllowlist asserts that the mail policy differs from the card
// policy by headings alone: scripts, event attributes, inline styles and script-like URL schemes
// stay blocked, so a custom admin template cannot smuggle markup into the fixed shell.
func TestRenderMailMarkdownKeepsTheCardAllowlist(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		forbidden []string
		required  []string
	}{
		{
			name:      "style element",
			src:       "<style>body{display:none}</style>\n\nText",
			forbidden: []string{"<style", "display:none"},
			required:  []string{"Text"},
		},
		{
			name:      "inline style attribute",
			src:       `<p style="color:red">x</p>`,
			forbidden: []string{"style=", "color:red"},
			required:  []string{"<p>x</p>"},
		},
		{
			name:      "script element",
			src:       "<script>alert(1)</script>",
			forbidden: []string{"<script", "alert(1)"},
		},
		{
			name:      "event handler",
			src:       `<h2 onclick="alert(1)">x</h2>`,
			forbidden: []string{"onclick", "alert(1)"},
			required:  []string{"<h2>x</h2>"},
		},
		{
			name:      "javascript link",
			src:       `[x](javascript:alert(1))`,
			forbidden: []string{"javascript:", "href="},
			required:  []string{"x"},
		},
		{
			name:     "tables and lists",
			src:      "| a | b |\n|---|---|\n| 1 | 2 |\n\n- one\n- two",
			required: []string{"<table>", "<th>a</th>", "<td>1</td>", "<ul>", "<li>one</li>"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := RenderMailMarkdown(tc.src)
			if err != nil {
				t.Fatalf("RenderMailMarkdown() error = %v", err)
			}
			for _, bad := range tc.forbidden {
				if strings.Contains(out, bad) {
					t.Errorf("output still contains forbidden %q:\n%s", bad, out)
				}
			}
			for _, want := range tc.required {
				if !strings.Contains(out, want) {
					t.Errorf("output lost required %q:\n%s", want, out)
				}
			}
			assertSafeFragmentAllowing(t, out, mailHeadingElements)
		})
	}
}
