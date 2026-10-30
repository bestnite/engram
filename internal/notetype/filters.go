// Copyright: Ankitects Pty Ltd and contributors
// License: GNU AGPL, version 3 or later; http://www.gnu.org/licenses/agpl.html
//
// Ported from rslib/src/template_filters.rs at tag 26.09.3 of ankitects/anki.
// The text-stripping helpers come from rslib/src/text.rs at the same tag.
//
// Anki can stop after the first filter it does not know and hand the rest to
// its Python renderer. Nothing here does that: the templates this package
// renders are always rendered completely, so an unknown filter is skipped and
// the value passes through unchanged.

package notetype

import (
	"encoding/hex"
	"html"
	"regexp"
	"strings"

	"github.com/zeebo/blake3"
)

// The hint element id is a BLAKE3 hash of the field text followed by the field
// name. Anki's hint filter builds it that way, and the golden output pins the
// exact id, so the hash has to be BLAKE3 rather than any other digest.
// github.com/zeebo/blake3 is the maintained pure-Go implementation used here;
// it is released into the public domain under CC0 1.0.

// applyFilters applies the filters of one replacement. filters is in the order
// the filters have to run, which is the reverse of the order they are written
// in: "{{text:hint:Front}}" parses to the key "Front" and the filters
// ["hint", "text"], so the hint is built first and then stripped of HTML.
func applyFilters(text string, filters []string, fieldName string, ctx *renderContext) string {
	// Anki folds the modifier into the type filter: the modifiers follow the
	// field name in the source text, so they are the first filters in this
	// slice.
	switch {
	case len(filters) == 2 && filters[0] == "cloze" && filters[1] == "type":
		filters = []string{"type-cloze"}
	case len(filters) == 2 && filters[0] == "nc" && filters[1] == "type":
		filters = []string{"type-nc"}
	case len(filters) > 0 && filters[len(filters)-1] == "type":
		filters = []string{"type"}
	}

	for _, filter := range filters {
		text = applyFilter(filter, text, fieldName, ctx)
	}
	return text
}

// applyFilter applies one filter. An unrecognised name returns the text
// unchanged, which is why a filter chain keeps working after an unknown step.
func applyFilter(filter, text, fieldName string, ctx *renderContext) string {
	switch filter {
	case "text":
		return stripHTML(text)
	case "furigana":
		return japaneseFilter(text, func(word, reading string) string {
			return "<ruby><rb>" + word + "</rb><rt>" + reading + "</rt></ruby>"
		})
	case "kanji":
		return japaneseFilter(text, func(word, reading string) string {
			return word
		})
	case "kana":
		return japaneseFilter(text, func(word, reading string) string {
			return reading
		})
	case "type":
		return "[[type:" + fieldName + "]]"
	case "type-cloze":
		return "[[type:cloze:" + fieldName + "]]"
	case "type-nc":
		return "[[type:nc:" + fieldName + "]]"
	case "hint":
		return hintFilter(text, fieldName)
	case "cloze":
		return clozeFilter(text, ctx)
	case "cloze-only":
		return clozeOnlyFilter(text, ctx)
	case "":
		// An empty filter name comes from a doubled colon and is ignored.
		return text
	}
	if options, ok := strings.CutPrefix(filter, "tts "); ok {
		return ttsFilter(options, text)
	}
	return text
}

// japaneseFilter replaces every "word[reading]" pair with the form the caller
// returns. A "sound:" tag inside the brackets is left alone, because it names
// an audio file rather than a reading.
func japaneseFilter(text string, format func(word, reading string) string) string {
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	return furiganaPattern.ReplaceAllStringFunc(text, func(match string) string {
		groups := furiganaPattern.FindStringSubmatch(match)
		word, reading := groups[1], groups[2]
		if strings.HasPrefix(reading, "sound:") {
			return match
		}
		return format(word, reading)
	})
}

var furiganaPattern = regexp.MustCompile(` ?([^ >]+?)\[(.+?)\]`)

// hintFilter builds the hint link and its hidden element. The element id is
// derived from the text and the field name so that two hints on one card get
// different ids and the same hint keeps its id across renders.
func hintFilter(text, fieldName string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	id := hintID(text, fieldName)
	return "\n<a class=hint href=\"#\"\n" +
		"onclick=\"this.style.display='none';\n" +
		"document.getElementById('hint" + id + "').style.display='block';\n" +
		"return false;\" draggable=false>\n" +
		fieldName + "</a>\n" +
		"<div id=\"hint" + id + "\" class=hint style=\"display: none\">" + text + "</div>\n"
}

// hintID returns the first eight bytes of the BLAKE3 hash of the text and the
// field name, in lower-case hex, which is the id Anki gives the hint element.
func hintID(text, fieldName string) string {
	sum := blake3.Sum256([]byte(text + fieldName))
	return hex.EncodeToString(sum[:8])
}

// ttsFilter marks text for text-to-speech synthesis. The options string already
// starts with the language, for example "en_US voices=Bob,Jane".
func ttsFilter(options, text string) string {
	return "[anki:tts lang=" + options + "]" + text + "[/anki:tts]"
}

// htmlPattern matches the markup Anki's text filter removes: comments, style
// and script elements including their contents, and every other tag.
var htmlPattern = regexp.MustCompile(`(?si)(<!--.*?-->)|(<style.*?>.*?</style>)|(<script.*?>.*?</script>)|(<.*?>)`)

// mathJaxPattern matches an inline or display math delimiter pair. LaTeX is
// written with HTML entities inside it, so the markup inside the delimiters is
// stripped but the entities are kept.
var mathJaxPattern = regexp.MustCompile(`(?is)(\\(?:\(|\[))(.*?)(\\(?:\]|\)))`)

func stripHTML(text string) string {
	return decodeEntities(stripHTMLPreservingEntities(text))
}

func stripHTMLPreservingEntities(text string) string {
	return htmlPattern.ReplaceAllString(text, "")
}

func stripHTMLInsideMathJax(text string) string {
	return mathJaxPattern.ReplaceAllStringFunc(text, func(match string) string {
		groups := mathJaxPattern.FindStringSubmatch(match)
		return groups[1] + stripHTMLPreservingEntities(groups[2]) + groups[3]
	})
}

// decodeEntities resolves HTML entities and turns the non-breaking space into
// an ordinary space, so a stripped field reads as plain text. Malformed entity
// references are left as they are.
func decodeEntities(text string) string {
	if !strings.Contains(text, "&") {
		return text
	}
	return strings.ReplaceAll(html.UnescapeString(text), "\u00a0", " ")
}

// encodeAttribute escapes a value for a double-quoted HTML attribute the way
// Anki's htmlescape dependency does: the five minimal entities, then every other
// ASCII character that is not a letter or digit and every character in the
// Latin-1 range as an upper-case hex reference. The oracle fixtures pin the
// exact form, for example a space becomes "&#x20;" and "|" becomes "&#x7C;".
func encodeAttribute(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		switch r {
		case '"':
			b.WriteString("&quot;")
		case '&':
			b.WriteString("&amp;")
		case '\'':
			b.WriteString("&#x27;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			if r < 256 && (r > 127 || !isASCIIAlnum(r)) {
				b.WriteString("&#x")
				b.WriteByte(upperHex[byte(r)>>4])
				b.WriteByte(upperHex[byte(r)&0x0f])
				b.WriteByte(';')
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

const upperHex = "0123456789ABCDEF"

func isASCIIAlnum(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}
