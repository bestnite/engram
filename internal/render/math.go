package render

import (
	"fmt"
	"strings"

	"git.nite07.com/nite/engram/internal/texmath"
)

// mathTokenPrefix is the prefix of a formula placeholder.
//
// The placeholder must be purely alphanumeric: goldmark does not rewrite it, and bluemonday
// does not treat it as a tag. A user's TeX would not normally contain this prefix; even if it
// did, the worst case is a misplaced restore, never a security problem.
const mathTokenPrefix = "MJXFORMULATOKEN"

// mathSpan records one piece of raw formula text replaced by a placeholder.
type mathSpan struct {
	token string
	raw   string
}

// escapeMath escapes only the three characters that would break HTML structure when the formula
// is written back into an HTML text node.
//
// The point is "do not escape backslashes, parentheses or braces": those are exactly the MathJax
// delimiters and TeX syntax characters, and turning them into entities such as &#92; would mean
// the browser textContent no longer holds the original TeX. The HTML parser turns &lt; back into
// <, so MathJax still ultimately reads the original TeX.
var mathEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
)

// protectMath replaces every whole formula in src with a placeholder, returning the rewritten
// string and the placeholder table. Where a formula starts and ends is decided by texmath, the
// same rule cardtype uses to decide "whether a cloze falls inside a formula".
func protectMath(src string) (string, []mathSpan) {
	ranges := texmath.Ranges(src)
	spans := make([]mathSpan, 0, len(ranges))
	var b strings.Builder
	last := 0
	for _, r := range ranges {
		token := fmt.Sprintf("%s%06d", mathTokenPrefix, len(spans))
		spans = append(spans, mathSpan{token: token, raw: src[r[0]:r[1]]})
		b.WriteString(src[last:r[0]])
		b.WriteString(token)
		last = r[1]
	}
	b.WriteString(src[last:])
	return b.String(), spans
}

// restoreMath swaps the placeholders in the rendered and sanitized HTML back for the raw formula text.
func restoreMath(out string, spans []mathSpan) string {
	for _, s := range spans {
		out = strings.ReplaceAll(out, s.token, mathEscaper.Replace(s.raw))
	}
	return out
}
