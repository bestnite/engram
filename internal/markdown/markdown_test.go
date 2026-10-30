package markdown

import (
	"strings"
	"testing"
)

// TestRender covers the two rendering contracts the deck description relies on: Markdown becomes HTML, and raw HTML in the
// source survives instead of being escaped, because the sandboxed frame is the security boundary rather than the renderer.
func TestRender(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
		deny []string
	}{
		{
			name: "heading and emphasis",
			src:  "# Heading\n\n**bold** and *italic*",
			want: []string{"<h1>Heading</h1>", "<strong>bold</strong>", "<em>italic</em>"},
		},
		{
			name: "raw HTML passes through",
			src:  "<p>kept</p>\n\n<script>window.parent.postMessage('x', '*')</script>",
			want: []string{"<p>kept</p>", "<script>window.parent.postMessage('x', '*')</script>"},
			deny: []string{"&lt;script&gt;"},
		},
		{
			name: "GFM table",
			src:  "| a | b |\n|---|---|\n| 1 | 2 |",
			want: []string{"<table>", "<td>1</td>"},
		},
		{
			name: "hard line wrap",
			src:  "first\nsecond",
			want: []string{"<br>"},
		},
		{
			name: "TeX formula survives",
			src:  `inline \(x^2\) formula`,
			want: []string{`\(x^2\)`},
			deny: []string{"MJXFORMULATOKEN"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Render(tc.src)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("Render(%q) = %q, want it to contain %q", tc.src, got, want)
				}
			}
			for _, deny := range tc.deny {
				if strings.Contains(got, deny) {
					t.Errorf("Render(%q) = %q, must not contain %q", tc.src, got, deny)
				}
			}
		})
	}
}

// TestRenderEmpty asserts an empty description renders empty instead of a paragraph, so a deck without a description shows
// nothing in the frame.
func TestRenderEmpty(t *testing.T) {
	got, err := Render("")
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if got != "" {
		t.Errorf("Render(\"\") = %q, want empty", got)
	}
}
