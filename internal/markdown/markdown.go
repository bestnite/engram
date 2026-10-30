// Package markdown renders Markdown source that is stored as text and displayed as HTML.
//
// A deck description is stored either as HTML or as Markdown, and a flag on the deck selects which. Rendering produces
// unsanitised HTML on purpose: the sandboxed frame is the security boundary, so raw HTML written in the source passes
// through unchanged, exactly like an HTML description.
package markdown

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"

	"git.nite07.com/nite/engram/internal/texmath"
)

// Renderer is the Markdown configuration every caller renders with: GFM extensions (tables, strikethrough, autolinks),
// hard line wraps, and raw HTML passed through.
var renderer = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(html.WithUnsafe(), html.WithHardWraps()),
)

// Render converts Markdown source to HTML.
//
// TeX formulas are replaced with alphanumeric placeholders before goldmark runs and restored afterwards: goldmark treats
// the backslash in \( and \[ as an escape and would strip it, while the frame typesets the surviving formula with MathJax.
func Render(src string) (string, error) {
	protected, spans := protectMath(src)
	var buf bytes.Buffer
	if err := renderer.Convert([]byte(protected), &buf); err != nil {
		return "", fmt.Errorf("render markdown: %w", err)
	}
	return restoreMath(buf.String(), spans), nil
}

// mathPrefix is the placeholder prefix. It is purely alphanumeric so goldmark leaves it untouched; a user's TeX would not
// normally contain it, and a misplaced restore is only a display quirk, never a correctness problem.
const mathPrefix = "MJXFORMULATOKEN"

// mathSpan records one raw formula replaced by a placeholder.
type mathSpan struct {
	token string
	raw   string
}

// mathEscaper escapes only the three characters that would break HTML structure when a formula is written back into a
// text node; backslashes, parentheses and braces must survive for MathJax to read the original TeX.
var mathEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
)

// protectMath replaces every whole formula in src with a placeholder, using the one rule for where a formula starts and
// ends (internal/texmath).
func protectMath(src string) (string, []mathSpan) {
	ranges := texmath.Ranges(src)
	spans := make([]mathSpan, 0, len(ranges))
	var b strings.Builder
	last := 0
	for _, r := range ranges {
		token := fmt.Sprintf("%s%06d", mathPrefix, len(spans))
		spans = append(spans, mathSpan{token: token, raw: src[r[0]:r[1]]})
		b.WriteString(src[last:r[0]])
		b.WriteString(token)
		last = r[1]
	}
	b.WriteString(src[last:])
	return b.String(), spans
}

// restoreMath swaps the placeholders in the rendered HTML back for the raw formula text.
func restoreMath(out string, spans []mathSpan) string {
	for _, s := range spans {
		out = strings.ReplaceAll(out, s.token, mathEscaper.Replace(s.raw))
	}
	return out
}
