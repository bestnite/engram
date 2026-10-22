// Package texmath recognizes the MathJax formula delimiters in raw card text.
//
// Why a separate package: render has to protect whole formulas before rendering Markdown,
// and cardtype has to know whether a cloze falls inside a formula. Both sides must decide
// "where a formula is" with the same rule, and render's tests depend on cardtype, so the
// rule can only live in a third place both can reference; two separate regexes would
// sooner or later disagree.
package texmath

import "regexp"

// pattern matches MathJax inline and block delimiters: \\( ... \\) and \\[ ... \\].
//
// (?s) lets . cross newlines, so block formulas may span multiple lines.
// (?:\\\\.|[^\\\\])*? allows backslash commands inside formulas (\\dfrac, \\int),
// while the non-greedy match stops at the first unescaped \\) or \\].
var pattern = regexp.MustCompile(
	`(?s)\\\((?:\\.|[^\\])*?\\\)|\\\[(?:\\.|[^\\])*?\\\]`,
)

// Ranges returns the byte range [start, end) of each formula in s, in order of
// appearance, delimiters included. It returns nil when there are no formulas.
func Ranges(s string) [][]int {
	return pattern.FindAllStringIndex(s, -1)
}

// Contains reports whether the byte range [start, end) lies entirely inside one formula.
// A range that only partly overlaps a formula does not count.
func Contains(ranges [][]int, start, end int) bool {
	for _, r := range ranges {
		if r[0] <= start && end <= r[1] {
			return true
		}
	}
	return false
}
