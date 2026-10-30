package store

// Markdown rendering for the one-time note type migration (migration 0012).
//
// The migration converts a stored Markdown field value into the HTML the new model stores. This file is the temporary
// pipeline it needs: goldmark with the GFM extensions, hard line wraps and raw HTML passed through, plus protection of
// TeX formulas so \( \) and \[ \] survive Markdown parsing. There is no sanitising pass, because the new content model
// stores unsanitised HTML and shows it only inside the sandboxed frame.
//
// The package that owned the old application-wide Markdown pipeline is gone, and new code must not depend on this one:
// it exists for the migration and nothing else.

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"

	"git.nite07.com/nite/engram/internal/texmath"
)

// migrationMarkdown is the Markdown configuration the migration renders with. The shortcode extension is deliberately
// absent: the migration rewrites {{tts:...}} and {{audio:...}} itself, so those markers must survive goldmark as text.
var migrationMarkdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(html.WithUnsafe(), html.WithHardWraps()),
)

// renderMigrationMarkdown converts one old Markdown/TeX field value to the HTML the new model stores.
func renderMigrationMarkdown(src string) (string, error) {
	protected, spans := protectMigrationMath(src)
	var buf bytes.Buffer
	if err := migrationMarkdown.Convert([]byte(protected), &buf); err != nil {
		return "", fmt.Errorf("render migration markdown: %w", err)
	}
	return restoreMigrationMath(buf.String(), spans), nil
}

// renderMigrationMarkdownInline converts one string-array item to inline HTML.
//
// An array item is a line, not a document: the block renderer would wrap it in <p>...</p>, and joining those wrappers
// with <br> would put a paragraph inside a line. The fragment inside the single paragraph is returned instead. An item
// that goldmark reads as an HTML block (for example a leading <div>) has no paragraph wrapper and is returned with only
// its trailing newline removed.
func renderMigrationMarkdownInline(src string) (string, error) {
	rendered, err := renderMigrationMarkdown(src)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rendered, "<p>") && strings.HasSuffix(rendered, "</p>\n") {
		return rendered[len("<p>") : len(rendered)-len("</p>\n")], nil
	}
	return strings.TrimSuffix(rendered, "\n"), nil
}

// migrationMathPrefix is the prefix of a formula placeholder. It must be purely alphanumeric so goldmark leaves it
// untouched; a user's TeX would not normally contain it, and a misplaced restore is never a correctness problem.
const migrationMathPrefix = "MJXFORMULATOKEN"

// migrationMathSpan records one raw formula replaced by a placeholder.
type migrationMathSpan struct {
	token string
	raw   string
}

// migrationMathEscaper escapes only the three characters that would break HTML structure when a formula is written
// back into a text node; backslashes, parentheses and braces must survive for MathJax to read the original TeX.
var migrationMathEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
)

// protectMigrationMath replaces every whole formula in src with a placeholder, using the one rule for where a formula
// starts and ends (internal/texmath).
func protectMigrationMath(src string) (string, []migrationMathSpan) {
	ranges := texmath.Ranges(src)
	spans := make([]migrationMathSpan, 0, len(ranges))
	var b strings.Builder
	last := 0
	for _, r := range ranges {
		token := fmt.Sprintf("%s%06d", migrationMathPrefix, len(spans))
		spans = append(spans, migrationMathSpan{token: token, raw: src[r[0]:r[1]]})
		b.WriteString(src[last:r[0]])
		b.WriteString(token)
		last = r[1]
	}
	b.WriteString(src[last:])
	return b.String(), spans
}

// restoreMigrationMath swaps the placeholders in the rendered HTML back for the raw formula text.
func restoreMigrationMath(out string, spans []migrationMathSpan) string {
	for _, s := range spans {
		out = strings.ReplaceAll(out, s.token, migrationMathEscaper.Replace(s.raw))
	}
	return out
}
