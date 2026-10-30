package mail

// Mail body rendering.
//
// TEMPORARY: this file is the minimal Markdown pipeline mail needs until the HTML mail templates land. It is a private
// copy of the old shared renderer's Markdown step: GitHub Flavored Markdown, a single newline inside a paragraph as
// <br>, raw HTML passed through, and TeX formulas protected so \( \) and \[ \] survive Markdown parsing.
//
// Two things are deliberately absent. There is no sanitising pass: the new content model does not sanitise, and the mail
// pipeline is replaced by admin-authored HTML in the same release. There is no shortcode element syntax: a mail has no
// client-side controls, so `{{tts:...}}`, `{{audio:...}}` and `{{ruby:...}}` degrade to their visible text, which is
// what the old renderer did for a mail body.

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"

	"git.nite07.com/nite/engram/internal/texmath"
)

// mailMarkdown is the shared goldmark instance for a mail body.
var mailMarkdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(html.WithUnsafe(), html.WithHardWraps()),
)

// renderMailMarkdown converts one mail body from Markdown to HTML.
func renderMailMarkdown(src string) (string, error) {
	protected, spans := protectMailMath(mailShortcodeText(src))
	var buf bytes.Buffer
	if err := mailMarkdown.Convert([]byte(protected), &buf); err != nil {
		return "", fmt.Errorf("render mail markdown: %w", err)
	}
	return restoreMailMath(buf.String(), spans), nil
}

// mailShortcodeText replaces every shortcode with the visible text it stands for: the body of a tts or audio shortcode,
// and "base(reading)" for a ruby annotation. An unknown name, a body without the name separator or an unclosed marker
// stays literal source text.
func mailShortcodeText(src string) string {
	var out strings.Builder
	for i := 0; i < len(src); {
		start := strings.Index(src[i:], "{{")
		if start < 0 {
			out.WriteString(src[i:])
			break
		}
		start += i
		out.WriteString(src[i:start])
		body, end, ok := balancedShortcode(src, start)
		if !ok {
			out.WriteString(src[start:])
			break
		}
		if text, known := shortcodeVisibleText(body); known {
			out.WriteString(text)
		} else {
			out.WriteString(src[start:end])
		}
		i = end
	}
	return out.String()
}

// balancedShortcode returns the text between the braces that open at start and the index just past the closing pair.
// Nested shortcodes are matched, so a body may contain another marker.
func balancedShortcode(src string, start int) (string, int, bool) {
	depth := 0
	for i := start; i < len(src); {
		switch {
		case strings.HasPrefix(src[i:], "{{"):
			depth++
			i += 2
		case strings.HasPrefix(src[i:], "}}"):
			depth--
			i += 2
			if depth == 0 {
				return src[start+2 : i-2], i, true
			}
		default:
			i++
		}
	}
	return "", 0, false
}

// shortcodeVisibleText maps one shortcode body (the text between the braces) to its visible form.
func shortcodeVisibleText(body string) (string, bool) {
	name, rest, ok := strings.Cut(body, ":")
	if !ok {
		return "", false
	}
	switch strings.TrimSpace(name) {
	case "tts", "audio":
		visible := rest
		if pipe := strings.IndexByte(visible, '|'); pipe >= 0 {
			visible = visible[:pipe]
		}
		return mailShortcodeText(visible), true
	case "ruby":
		base, reading, ok := strings.Cut(rest, "|")
		if !ok {
			return "", false
		}
		return mailShortcodeText(base) + "(" + strings.TrimSpace(reading) + ")", true
	default:
		return "", false
	}
}

// mailMathPrefix is the prefix of a formula placeholder; it is purely alphanumeric so goldmark leaves it alone.
const mailMathPrefix = "MJXFORMULATOKEN"

// mailMathSpan records one raw formula replaced by a placeholder.
type mailMathSpan struct {
	token string
	raw   string
}

// mailMathEscaper escapes only the three characters that would break HTML structure in a text node.
var mailMathEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
)

// protectMailMath replaces every whole formula in src with a placeholder.
func protectMailMath(src string) (string, []mailMathSpan) {
	ranges := texmath.Ranges(src)
	spans := make([]mailMathSpan, 0, len(ranges))
	var b strings.Builder
	last := 0
	for _, r := range ranges {
		token := fmt.Sprintf("%s%06d", mailMathPrefix, len(spans))
		spans = append(spans, mailMathSpan{token: token, raw: src[r[0]:r[1]]})
		b.WriteString(src[last:r[0]])
		b.WriteString(token)
		last = r[1]
	}
	b.WriteString(src[last:])
	return b.String(), spans
}

// restoreMailMath swaps the placeholders in the rendered HTML back for the raw formula text.
func restoreMailMath(out string, spans []mailMathSpan) string {
	for _, s := range spans {
		out = strings.ReplaceAll(out, s.token, mailMathEscaper.Replace(s.raw))
	}
	return out
}
