// Copyright: Ankitects Pty Ltd and contributors
// License: GNU AGPL, version 3 or later; http://www.gnu.org/licenses/agpl.html
//
// Ported from rslib/src/card_rendering/parser.rs, writer.rs and tts/mod.rs at
// tag 26.09.3 of ankitects/anki. The replay-button markup is taken from
// qt/aqt/sound.py at the same tag, which is where Anki's Python package turns a
// play marker into a button; the tag argument of ReplayButtonHTML does not
// change that markup.
//
// The parser is the whole of Anki's audio/video layer: it splits rendered card
// text into plain text, [sound:name] nodes and [anki:<name> ...]...[/anki:<name>]
// directives, and the writers below either extract the audio tags, strip them or
// re-serialize the directives that are not audio.
//
// Two details of the Rust original are deliberately different:
//
//   - Anki stores a directive's unknown options in a hash map, so the order in
//     OtherArgs is not defined. This port keeps the order the options appear in
//     the card text, which makes the output reproducible.
//   - Anki renders a localized error instead of a play marker when a TTS tag has
//     no lang, and substitutes a localized word for "[...]" when a TTS tag has no
//     cloze_blank option. Both strings come from the translation catalog, which a
//     leaf package may not reach, so this layer always extracts the tag and only
//     substitutes "[...]" when cloze_blank is present. The collection layer that
//     owns the catalog renders the error and the placeholder.

package notetype

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// AVTag is one audio or text-to-speech reference found in rendered card text.
//
// Kind is "sound" for a [sound:name] tag and "tts" for an
// [anki:tts ...]...[/anki:tts] directive. A sound tag only sets Name; a TTS tag
// sets Lang, Voices, Speed, FieldText and possibly ClozeBlank and OtherArgs.
type AVTag struct {
	Kind string `json:"kind"`
	// Side is the card side the tag was extracted from: "q" for the question and "a" for the answer. The answer
	// face carries the question's tags as well, because {{FrontSide}} inserts text whose markers still address them.
	Side string `json:"side"`
	// Name is the filename inside a sound tag, with HTML entities decoded.
	Name string `json:"name,omitempty"`
	// Lang is the TTS language, for example "en_US".
	Lang string `json:"lang,omitempty"`
	// Voices lists the requested TTS voices in the order the option names them.
	Voices []string `json:"voices,omitempty"`
	// Speed is the requested speech rate. Anki's default, also used for an
	// option that does not parse as a number, is 1.0.
	Speed float64 `json:"speed"`
	// OtherArgs holds every TTS option this port does not interpret, as
	// "key=value", in the order the options appear in the card text.
	OtherArgs []string `json:"other_args,omitempty"`
	// FieldText is the text to speak: the directive body with HTML removed.
	FieldText string `json:"field_text,omitempty"`
	// ClozeBlank is the value of the cloze_blank option, or nil when the option
	// is absent. In a cloze TTS tag it replaces every "[...]" in FieldText.
	ClozeBlank *string `json:"cloze_blank,omitempty"`
}

// nodeKind is the kind of one parsed card-text node.
type nodeKind int

const (
	nodeText nodeKind = iota
	nodeSound
	nodeDirective
)

// avOption is one whitespace-separated key=value pair of a directive's opening
// tag.
type avOption struct {
	key   string
	value string
}

// avNode is one node of the parsed card text. text holds the literal text of a
// text node, the resource of a sound node, or the body of a directive; name and
// options are set for directives only.
type avNode struct {
	kind    nodeKind
	text    string
	name    string
	options []avOption
}

// ExtractAVTags replaces every audio and TTS tag in html with Anki's
// [anki:play:<side>:<n>] marker and returns the rewritten HTML and the tags in
// document order. Numbering restarts at zero for each call, which is what makes
// the question and the answer side two separate marker sequences.
//
// A sound tag nested inside a TTS body is not extracted: the TTS directive is
// one tag and the sound stays in its field text.
func ExtractAVTags(html string, side string) (string, []AVTag) {
	var written strings.Builder
	var tags []AVTag
	for _, node := range parseAVNodes(html) {
		switch node.kind {
		case nodeText:
			written.WriteString(node.text)
		case nodeSound:
			written.WriteString(playMarker(side, len(tags)))
			tags = append(tags, AVTag{Kind: "sound", Side: side, Name: decodeEntities(node.text)})
		case nodeDirective:
			if node.name != "tts" {
				written.WriteString(node.directiveMarkup())
				continue
			}
			written.WriteString(playMarker(side, len(tags)))
			tag := node.ttsTag()
			tag.Side = side
			tags = append(tags, tag)
		}
	}
	return written.String(), tags
}

// ReplayButtonHTML renders the replay button that Anki's desktop frame replaces
// a play marker with. side and index identify the marker: [anki:play:<side>:<index>].
//
// The marker form is the server-side contract of ExtractAVTags, because a card's
// HTML is rendered without knowing whether a frame can play audio. This element
// is the frame's side of that contract: Anki's Python package substitutes it for
// the marker on desktop, and a client that can play audio substitutes its own
// element for the same marker.
//
// The markup is identical for sound and TTS tags, so tag does not affect the
// result; it is accepted so a caller passes the tag it is about to play. The
// result starts with a newline and ends with the closing anchor, exactly as the
// string Anki substitutes for the marker.
func ReplayButtonHTML(tag AVTag, side string, index int) string {
	return "\n" +
		`<a class="replay-button soundLink" href=# onclick="pycmd('play:` + side + ":" + strconv.Itoa(index) + `'); return false;" draggable="false">` + "\n" +
		`    <svg class="playImage" viewBox="0 0 64 64" version="1.1">` + "\n" +
		`        <circle cx="32" cy="32" r="29" />` + "\n" +
		`        <path d="M56.502,32.301l-37.502,20.101l0.329,-40.804l37.173,20.703Z" />` + "\n" +
		`    </svg>` + "\n" +
		`</a>`
}

// playMarker is the marker Anki writes in place of a tag. The frame replaces it
// with a play control and passes the same side and index back when it is
// clicked.
func playMarker(side string, index int) string {
	return "[anki:play:" + side + ":" + strconv.Itoa(index) + "]"
}

// stripAVTags removes sound tags and entire TTS directives from html, leaving
// text nodes and every other directive in place. This is Anki's
// strip_av_tags(), the helper PlainText builds on.
func stripAVTags(html string) string {
	var written strings.Builder
	for _, node := range parseAVNodes(html) {
		switch node.kind {
		case nodeText:
			written.WriteString(node.text)
		case nodeSound:
			// A sound tag is audio only and contributes no text.
		case nodeDirective:
			if node.name == "tts" {
				// The TTS body is audio only, unlike other directives, whose
				// body is text the card shows.
				continue
			}
			written.WriteString(node.directiveMarkup())
		}
	}
	return written.String()
}

// ttsTag turns a TTS directive into the structured tag Anki extracts. Speed is
// parsed as a float and falls back to Anki's 1.0 when the option is missing or
// malformed.
func (node avNode) ttsTag() AVTag {
	tag := AVTag{Kind: "tts", Speed: 1}
	for _, option := range node.options {
		switch option.key {
		case "lang":
			tag.Lang = option.value
		case "voices":
			tag.Voices = strings.Split(option.value, ",")
		case "speed":
			if speed, err := strconv.ParseFloat(option.value, 64); err == nil {
				tag.Speed = speed
			}
		case "cloze_blank":
			blank := option.value
			tag.ClozeBlank = &blank
		default:
			tag.OtherArgs = append(tag.OtherArgs, option.key+"="+option.value)
		}
	}
	tag.FieldText = stripHTMLForTTS(node.text)
	if tag.ClozeBlank != nil {
		tag.FieldText = strings.ReplaceAll(tag.FieldText, "[...]", *tag.ClozeBlank)
	}
	return tag
}

// directiveMarkup writes a non-TTS directive back in normalized form, which is
// what Anki does for directives it does not interpret.
func (node avNode) directiveMarkup() string {
	var written strings.Builder
	written.WriteString("[anki:")
	written.WriteString(node.name)
	for _, option := range node.options {
		written.WriteString(optionMarkup(option.key, option.value))
	}
	written.WriteString("]")
	written.WriteString(node.text)
	written.WriteString("[/anki:")
	written.WriteString(node.name)
	written.WriteString("]")
	return written.String()
}

// optionMarkup quotes a value that would otherwise end the opening tag or split
// the option, which is Anki's writer rule.
func optionMarkup(key, value string) string {
	if strings.ContainsAny(value, "] \t\r\n") {
		return " " + key + `="` + value + `"`
	}
	return " " + key + "=" + value
}

// htmlLinebreakTags matches the elements Anki's TTS text turns into a space
// before stripping the rest of the HTML. Without this step a line break would
// join the words on both sides of it.
var htmlLinebreakTags = regexp.MustCompile(`(?i)</?(?:br|address|article|aside|blockquote|canvas|dd|div|dl|dt|fieldset|figcaption|figure|footer|form|h[1-6]|header|hr|li|main|nav|noscript|ol|output|p|pre|section|table|tfoot|ul|video)>`)

// stripHTMLForTTS is Anki's strip_html_for_tts(): block boundaries become one
// space, then all remaining markup and entities are resolved.
func stripHTMLForTTS(html string) string {
	return stripHTML(htmlLinebreakTags.ReplaceAllString(html, " "))
}

// parseAVNodes splits txt into nodes until nothing is left. Every step consumes
// at least one byte, so the loop always terminates.
func parseAVNodes(txt string) []avNode {
	var nodes []avNode
	for txt != "" {
		node, rest, ok := parseAVNode(txt)
		if !ok {
			break
		}
		nodes = append(nodes, node)
		txt = rest
	}
	return nodes
}

// parseAVNode parses one node: a sound tag, a directive, or literal text.
func parseAVNode(txt string) (avNode, string, bool) {
	if node, rest, ok := parseAVSoundNode(txt); ok {
		return node, rest, true
	}
	if node, rest, ok := parseAVTagNode(txt); ok {
		return node, rest, true
	}
	return parseAVTextNode(txt)
}

// parseAVSoundNode parses [sound:name]. The name must be non-empty and free of
// "]", which is Anki's is_not("]") rule; [sound:] is therefore plain text.
func parseAVSoundNode(txt string) (avNode, string, bool) {
	const prefix = "[sound:"
	if !strings.HasPrefix(txt, prefix) {
		return avNode{}, txt, false
	}
	rest := txt[len(prefix):]
	end := strings.IndexByte(rest, ']')
	if end <= 0 {
		return avNode{}, txt, false
	}
	return avNode{kind: nodeSound, text: rest[:end]}, rest[end+1:], true
}

// parseAVTagNode parses [anki:name option=value ...]body[/anki:name]. A missing
// or mismatched closing tag makes the whole thing plain text, as in Anki.
func parseAVTagNode(txt string) (avNode, string, bool) {
	const prefix = "[anki:"
	if !strings.HasPrefix(txt, prefix) {
		return avNode{}, txt, false
	}
	name, nameLength, ok := avDirectiveName(txt[len(prefix):])
	if !ok {
		return avNode{}, txt, false
	}
	rest := trimAVSpace(txt[len(prefix)+nameLength:])
	options, rest, _ := parseAVOptions(rest)
	if !strings.HasPrefix(rest, "]") {
		return avNode{}, txt, false
	}
	rest = rest[1:]
	closing := "[/anki:" + name + "]"
	end := strings.Index(rest, closing)
	if end < 0 {
		return avNode{}, txt, false
	}
	return avNode{kind: nodeDirective, name: name, text: rest[:end], options: options}, rest[end+len(closing):], true
}

// parseAVTextNode consumes at least one character and stops at the "[" that may
// start the next tag. The first character is always consumed, so a "[" that does
// not start a valid tag stays in the text instead of stalling the parser.
func parseAVTextNode(txt string) (avNode, string, bool) {
	_, size := utf8.DecodeRuneInString(txt)
	if end := strings.IndexByte(txt[size:], '['); end >= 0 {
		end += size
		return avNode{kind: nodeText, text: txt[:end]}, txt[end:], true
	}
	return avNode{kind: nodeText, text: txt}, "", true
}

// avDirectiveName reads a directive name, which is everything up to "]",
// whitespace or the end of the opening tag.
func avDirectiveName(txt string) (string, int, bool) {
	for index := 0; index < len(txt); index++ {
		switch txt[index] {
		case ']', ' ', '\t', '\r', '\n':
			if index == 0 {
				return "", 0, false
			}
			return txt[:index], index, true
		}
	}
	if txt == "" {
		return "", 0, false
	}
	return txt, len(txt), true
}

// parseAVOptions reads whitespace-separated key=value pairs. A pair that does
// not start with a key and "=" ends the list, and the whitespace before it is
// left for the caller: Anki's parser fails the whole opening tag in that case,
// so "[anki:foo bar]" is text while "[anki:foo bar=baz ]" is a directive.
func parseAVOptions(txt string) ([]avOption, string, bool) {
	var options []avOption
	for {
		start := trimAVSpace(txt)
		key, keyLength, ok := avOptionKey(start)
		if !ok || !strings.HasPrefix(start[keyLength:], "=") {
			return options, txt, true
		}
		value, valueLength, ok := avOptionValue(start[keyLength+1:])
		if !ok {
			return options, txt, true
		}
		options = append(options, avOption{key: key, value: value})
		txt = trimAVSpace(start[keyLength+1+valueLength:])
	}
}

// avOptionKey reads an option name, which ends at "]", whitespace or "=".
func avOptionKey(txt string) (string, int, bool) {
	for index := 0; index < len(txt); index++ {
		switch txt[index] {
		case ']', ' ', '\t', '\r', '\n', '=':
			if index == 0 {
				return "", 0, false
			}
			return txt[:index], index, true
		}
	}
	if txt == "" {
		return "", 0, false
	}
	return txt, len(txt), true
}

// avOptionValue reads an option value, either double-quoted or up to "]", a
// quote or whitespace. An unquoted value may be empty.
func avOptionValue(txt string) (string, int, bool) {
	if strings.HasPrefix(txt, `"`) {
		end := strings.IndexByte(txt[1:], '"')
		if end < 0 {
			return "", 0, false
		}
		return txt[1 : 1+end], end + 2, true
	}
	for index := 0; index < len(txt); index++ {
		switch txt[index] {
		case ']', ' ', '\t', '\r', '\n', '"':
			return txt[:index], index, true
		}
	}
	return txt, len(txt), true
}

// trimAVSpace removes the characters Anki's multispace0 matches.
func trimAVSpace(txt string) string {
	for index := 0; index < len(txt); index++ {
		switch txt[index] {
		case ' ', '\t', '\r', '\n':
		default:
			return txt[index:]
		}
	}
	return ""
}
