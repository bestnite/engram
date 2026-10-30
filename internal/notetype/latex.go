// Copyright: Ankitects Pty Ltd and contributors
// License: GNU AGPL, version 3 or later; http://www.gnu.org/licenses/agpl.html
//
// Ported from rslib/src/latex.rs at tag 26.09.3 of ankitects/anki.
//
// Anki replaces embedded LaTeX with an image reference whose name is derived
// from the TeX source, so the name is stable across renders and two cards that
// share an expression share one file. The image itself is generated later, by
// the collection layer that owns the TeX toolchain; this file only rewrites the
// marker and computes the name.

package notetype

import (
	"crypto/sha1"
	"encoding/hex"
	"regexp"
	"strings"
)

// latexNewlinePattern matches the markup Anki turns into a line break before the
// TeX source is hashed, because a newline inside TeX is significant.
var latexNewlinePattern = regexp.MustCompile(`(?i)<br( /)?>|<div>`)

// RenderLatex replaces every [latex], [$] and [$$] pair in html with an image
// reference to the file that holds the rendered TeX. latexSVG selects the .svg
// extension; otherwise the extension is .png.
//
// latexPattern is defined in cloze.go, where the LaTeX detector from the same
// Anki file uses it: detection and extraction must agree on what an expression
// is, so the two share one expression.
func RenderLatex(html string, latexSVG bool) string {
	matches := latexPattern.FindAllStringSubmatchIndex(html, -1)
	if matches == nil {
		return html
	}
	var rendered strings.Builder
	last := 0
	for _, match := range matches {
		rendered.WriteString(html[last:match[0]])
		var tex string
		switch {
		case match[2] >= 0:
			tex = html[match[2]:match[3]]
		case match[4] >= 0:
			tex = "$" + html[match[4]:match[5]] + "$"
		default:
			tex = `\begin{displaymath}` + html[match[6]:match[7]] + `\end{displaymath}`
		}
		rendered.WriteString(latexImage(stripLatexMarkup(tex), latexSVG))
		last = match[1]
	}
	rendered.WriteString(html[last:])
	return rendered.String()
}

// stripLatexMarkup reduces the TeX source to the text that is hashed and shown
// as the image's alt text: block boundaries become a real newline, then all
// remaining markup is removed and entities are resolved. Without the newline
// step the words on both sides of a <br> would be joined.
func stripLatexMarkup(tex string) string {
	return stripHTML(latexNewlinePattern.ReplaceAllString(tex, "\n"))
}

// latexImage is the image reference Anki writes for a TeX source. The file name
// is latex-<sha1 of the TeX source>.<ext>; the alt text is the TeX source encoded
// as an attribute, which is why punctuation there appears as hex references.
func latexImage(tex string, svg bool) string {
	extension := "png"
	if svg {
		extension = "svg"
	}
	sum := sha1.Sum([]byte(tex))
	var reference strings.Builder
	reference.WriteString(`<img class=latex alt="`)
	reference.WriteString(encodeAttribute(tex))
	reference.WriteString(`" src="latex-`)
	reference.WriteString(hex.EncodeToString(sum[:]))
	reference.WriteByte('.')
	reference.WriteString(extension)
	reference.WriteString(`">`)
	return reference.String()
}
