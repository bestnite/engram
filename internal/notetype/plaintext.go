// Copyright: Ankitects Pty Ltd and contributors
// License: GNU AGPL, version 3 or later; http://www.gnu.org/licenses/agpl.html
//
// Ported from rslib/src/text.rs (html_to_text_line and its helpers) at tag
// 26.09.3 of ankitects/anki.

package notetype

import (
	"regexp"
	"strings"
)

// plainTextSpacers matches the markup Anki turns into one space before it strips
// the rest: a line break, an opening <div>, and a literal newline. No other block
// element is a separator, so "<p>one</p><p>two</p>" loses the word boundary.
var plainTextSpacers = regexp.MustCompile(`(?i)<br\s*/?>|<div>|\n`)

// plainTextTypeTag matches the type-in placeholder, which is not text.
var plainTextTypeTag = regexp.MustCompile(`\[\[type:[^]]+\]\]`)

// plainTextSoundTag matches a sound tag. Anki's html_to_text_line removes these
// itself, with or without a preceding AV parse.
var plainTextSoundTag = regexp.MustCompile(`\[sound:([^]]+)\]`)

// PlainText returns the single-line plain-text form of rendered card HTML, the
// form a field list, a search index or a CSV export shows.
//
// The result is one line: Anki maps a <br>, an opening <div> and a literal
// newline to one space, drops every other tag and HTML comment, decodes HTML
// entities (a non-breaking space becomes an ordinary space), and trims the ends.
// Adjacent text with no separator between it is joined, so "<p>one</p><p>two</p>"
// becomes "onetwo" - the card's own markup decides where a space belongs.
//
// Audio and TTS tags contribute no text and are removed before the markup is
// stripped. That includes the whole [anki:tts]...[/anki:tts] directive: its body
// is spoken, not shown. Anki's html_to_text_line removes [sound:] tags but leaves
// the TTS directive in the text, because its caller has already stripped the AV
// tags; this function does that stripping itself so the name means what it says.
// Any other [anki:...] directive is left in place, as in Anki.
func PlainText(html string) string {
	text := stripAVTags(html)
	text = plainTextSpacers.ReplaceAllString(text, " ")
	text = plainTextTypeTag.ReplaceAllString(text, "")
	text = plainTextSoundTag.ReplaceAllString(text, "")
	return strings.TrimSpace(stripHTML(text))
}
