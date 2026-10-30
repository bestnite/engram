// Copyright: Ankitects Pty Ltd and contributors
// License: GNU AGPL, version 3 or later; http://www.gnu.org/licenses/agpl.html
//
// Ported from rslib/src/cloze.rs at tag 26.09.3 of ankitects/anki, together
// with the image-occlusion data helpers from rslib/src/image_occlusion/imageocclusion.rs
// and the LaTeX detector from rslib/src/latex.rs at the same tag.
//
// The port covers the whole file: the tokenizer, the front/back reveal of one
// cloze number (including nesting and image occlusions), the cloze and
// cloze-only filters, the cloze-number set of a note's fields, the text a
// type-in cloze answer is compared against, the LaTeX expansion used before
// LaTeX is extracted, and the structured shapes the image-occlusion editor
// reads.
//
// `|` needs no special handling: Anki's tokenizer only recognises "{{c",
// "::" and "}}" inside a cloze, so a pipe is ordinary text and the attribute
// escaping later turns it into "&#x7C;" on the question side.

package notetype

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// clozeNode is one piece of a field value: either literal text, or a cloze
// deletion with its own children (a cloze may contain another cloze).
type clozeNode struct {
	text  string
	cloze *clozeDeletion
}

// clozeDeletion is one {{c1::text}} or {{c1::text::hint}} occurrence. Ordinals
// holds every number written in the marker ("{{c1,3::x}}" has 1 and 3).
type clozeDeletion struct {
	ordinals []int
	children []clozeNode
	hint     string
	hasHint  bool
}

const (
	clozeOpenMarker  = "{{c"
	clozeCloseMarker = "}}"
	clozeImagePrefix = "image-occlusion:"
	// Anki refuses to nest deeper than this, so a malformed field cannot make
	// the parser consume unbounded memory.
	clozeMaxNesting = 10
)

type clozeToken struct {
	open     bool
	ordinals []int
	text     string
	closing  bool
}

// parseClozeOpen recognises "{{c<numbers>::" at the start of s.
func parseClozeOpen(s string) (ordinals []int, rest string, ok bool) {
	if !strings.HasPrefix(s, clozeOpenMarker) {
		return nil, s, false
	}
	i := len(clozeOpenMarker)
	start := i
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == ',') {
		i++
	}
	numbers := s[start:i]
	for _, part := range strings.Split(numbers, ",") {
		value, err := strconv.ParseUint(part, 10, 16)
		if err != nil {
			continue
		}
		ordinals = append(ordinals, int(value))
	}
	ordinals = uniqueSorted(ordinals)
	if len(ordinals) == 0 {
		return nil, s, false
	}
	if !strings.HasPrefix(s[i:], "::") {
		return nil, s, false
	}
	return ordinals, s[i+2:], true
}

func uniqueSorted(values []int) []int {
	if len(values) == 0 {
		return nil
	}
	sort.Ints(values)
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

// clozeTokens splits a field value into cloze markers and literal text. A
// closing marker outside any deletion stays literal text, as it does in Anki.
func clozeTokens(text string) []clozeToken {
	var tokens []clozeToken
	for len(text) > 0 {
		if ordinals, rest, ok := parseClozeOpen(text); ok {
			tokens = append(tokens, clozeToken{open: true, ordinals: ordinals})
			text = rest
			continue
		}
		if strings.HasPrefix(text, clozeCloseMarker) {
			tokens = append(tokens, clozeToken{closing: true})
			text = text[len(clozeCloseMarker):]
			continue
		}
		// Find the next position that starts a marker; everything before it is
		// literal text. The markers are ASCII, so scanning bytes cannot split a
		// multi-byte character.
		index := len(text)
		for i := 1; i < len(text); i++ {
			if strings.HasPrefix(text[i:], clozeCloseMarker) {
				index = i
				break
			}
			if _, _, ok := parseClozeOpen(text[i:]); ok {
				index = i
				break
			}
		}
		tokens = append(tokens, clozeToken{text: text[:index]})
		text = text[index:]
	}
	return tokens
}

// parseClozes builds the node tree of a field value. Deeper nesting than Anki
// allows is ignored, which turns an over-nested marker into literal text.
func parseClozes(text string) []clozeNode {
	var open []*clozeDeletion
	var output []clozeNode
	for _, token := range clozeTokens(text) {
		switch {
		case token.open:
			if len(open) < clozeMaxNesting {
				open = append(open, &clozeDeletion{ordinals: token.ordinals})
			}
		case token.closing:
			if len(open) == 0 {
				output = append(output, clozeNode{text: clozeCloseMarker})
				continue
			}
			deletion := open[len(open)-1]
			open = open[:len(open)-1]
			if len(open) == 0 {
				output = append(output, clozeNode{cloze: deletion})
				continue
			}
			parent := open[len(open)-1]
			parent.children = append(parent.children, clozeNode{cloze: deletion})
		default:
			literal := token.text
			if len(open) > 0 {
				deletion := open[len(open)-1]
				// The hint follows the second "::" of the deletion, but an
				// image-occlusion marker uses colons in its own syntax.
				if !strings.HasPrefix(literal, clozeImagePrefix) {
					if head, tail, found := strings.Cut(literal, "::"); found {
						literal = head
						deletion.hint = tail
						deletion.hasHint = true
					}
				}
				deletion.children = append(deletion.children, clozeNode{text: literal})
			} else {
				output = append(output, clozeNode{text: literal})
			}
		}
	}
	// A deletion that is never closed is dropped, as Anki drops it: its content
	// only ever lived inside the marker that never terminated.
	return output
}

func (d *clozeDeletion) hintText() string {
	if d.hasHint {
		return d.hint
	}
	return "..."
}

func (d *clozeDeletion) containsOrdinal(ordinal int) bool {
	for _, candidate := range d.ordinals {
		if candidate == ordinal {
			return true
		}
	}
	return false
}

func (d *clozeDeletion) ordinalsText() string {
	parts := make([]string, len(d.ordinals))
	for i, ordinal := range d.ordinals {
		parts[i] = strconv.Itoa(ordinal)
	}
	return strings.Join(parts, ",")
}

func (d *clozeDeletion) clozedText() string {
	if len(d.children) == 1 && d.children[0].cloze == nil {
		return d.children[0].text
	}
	var b strings.Builder
	for _, child := range d.children {
		if child.cloze != nil {
			b.WriteString(child.cloze.clozedText())
		} else {
			b.WriteString(child.text)
		}
	}
	return b.String()
}

// imageOcclusionText returns the shape description of an image-occlusion
// deletion, or false when the deletion is ordinary cloze text.
func (d *clozeDeletion) imageOcclusionText() (string, bool) {
	if len(d.children) == 0 || d.children[0].cloze != nil {
		return "", false
	}
	return strings.CutPrefix(d.children[0].text, clozeImagePrefix)
}

// revealClozeText renders a field value for one cloze number. On the question
// side the active deletion is hidden; on the answer side it is shown. The
// result is empty when the field holds no deletion for that number, which is
// how Anki reports a card whose cloze disappeared.
func revealClozeText(text string, ordinal int, question bool) string {
	var b strings.Builder
	found := false
	for _, node := range parseClozes(text) {
		if node.cloze == nil {
			b.WriteString(node.text)
			continue
		}
		revealCloze(&b, node.cloze, ordinal, question, &found)
	}
	if !found {
		return ""
	}
	return b.String()
}

func revealCloze(b *strings.Builder, deletion *clozeDeletion, ordinal int, question bool, found *bool) {
	active := deletion.containsOrdinal(ordinal)
	if active {
		*found = true
	}

	if shape, isImage := deletion.imageOcclusionText(); isImage {
		b.WriteString(renderImageOcclusion(shape, question, active, deletion.ordinals))
		return
	}

	switch {
	case question && active:
		// The question hides an active deletion, but keeps its content as the
		// data-cloze attribute so the client can animate it back in.
		var content strings.Builder
		for _, child := range deletion.children {
			if child.cloze != nil {
				revealCloze(&content, child.cloze, ordinal, question, found)
			} else {
				content.WriteString(child.text)
			}
		}
		b.WriteString(`<span class="cloze" data-cloze="`)
		b.WriteString(encodeAttribute(content.String()))
		b.WriteString(`" data-ordinal="`)
		b.WriteString(deletion.ordinalsText())
		b.WriteString(`">[`)
		b.WriteString(deletion.hintText())
		b.WriteString(`]</span>`)
	case !question && active:
		b.WriteString(`<span class="cloze" data-ordinal="`)
		b.WriteString(deletion.ordinalsText())
		b.WriteString(`">`)
		for _, child := range deletion.children {
			if child.cloze != nil {
				revealCloze(b, child.cloze, ordinal, question, found)
			} else {
				b.WriteString(child.text)
			}
		}
		b.WriteString(`</span>`)
	default:
		b.WriteString(`<span class="cloze-inactive" data-ordinal="`)
		b.WriteString(deletion.ordinalsText())
		b.WriteString(`">`)
		for _, child := range deletion.children {
			if child.cloze != nil {
				revealCloze(b, child.cloze, ordinal, question, found)
			} else {
				b.WriteString(child.text)
			}
		}
		b.WriteString(`</span>`)
	}
}

// revealClozeTextOnly keeps only the text of the deletions for one number, in
// the order they appear, and joins multiple matches with ", ". The cloze-only
// filter renders the answer of a type-in cloze card this way.
func revealClozeTextOnly(text string, ordinal int, question bool) string {
	var parts []string
	for _, node := range parseClozes(text) {
		collectClozeText(node, ordinal, question, &parts)
	}
	return strings.Join(parts, ", ")
}

func collectClozeText(node clozeNode, ordinal int, question bool, parts *[]string) {
	if node.cloze == nil {
		return
	}
	deletion := node.cloze
	if deletion.containsOrdinal(ordinal) {
		// Reuse the reveal helper so a nested deletion shows its hint or its
		// text exactly as the cloze filter would.
		var b strings.Builder
		if question {
			b.WriteString(deletion.hintText())
		} else {
			b.WriteString(deletion.clozedText())
		}
		*parts = append(*parts, b.String())
	}
	for _, child := range deletion.children {
		collectClozeText(child, ordinal, question, parts)
	}
}

// clozeNumbersIn returns the set of cloze numbers a field value holds. Number
// zero is an image-occlusion placeholder and is not a card number.
func clozeNumbersIn(text string) map[int]bool {
	numbers := make(map[int]bool)
	var walk func(nodes []clozeNode)
	walk = func(nodes []clozeNode) {
		for _, node := range nodes {
			if node.cloze == nil {
				continue
			}
			for _, ordinal := range node.cloze.ordinals {
				if ordinal != 0 {
					numbers[ordinal] = true
				}
			}
			walk(node.cloze.children)
		}
	}
	walk(parseClozes(text))
	return numbers
}

// clozeFilter is the "cloze" template filter.
func clozeFilter(text string, ctx *renderContext) string {
	return stripHTMLInsideMathJax(revealClozeText(text, ctx.cardOrdinal, ctx.onFront))
}

// clozeOnlyFilter is the "cloze-only" template filter.
func clozeOnlyFilter(text string, ctx *renderContext) string {
	return revealClozeTextOnly(text, ctx.cardOrdinal, ctx.onFront)
}

// renderImageOcclusion emits the shape markup of an image-occlusion deletion.
// The question hides the active shape; the answer highlights it.
func renderImageOcclusion(shape string, question, active bool, ordinals []int) string {
	data := imageClozeData(shape)
	switch {
	case question && active, containsInt(ordinals, 0):
		return `<div class="cloze" data-ordinal="` + ordinalsText(ordinals) + `" ` + data + `></div>`
	case !active:
		return `<div class="cloze-inactive" data-ordinal="` + ordinalsText(ordinals) + `" ` + data + `></div>`
	case !question && active:
		return `<div class="cloze-highlight" data-ordinal="` + ordinalsText(ordinals) + `" ` + data + `></div>`
	default:
		return ""
	}
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func ordinalsText(ordinals []int) string {
	parts := make([]string, len(ordinals))
	for i, ordinal := range ordinals {
		parts[i] = strconv.Itoa(ordinal)
	}
	return strings.Join(parts, ",")
}

// ImageOcclusionProperty is one "name=value" pair of an image-occlusion shape,
// for example {"left", "0.1"}.
type ImageOcclusionProperty struct {
	Name  string
	Value string
}

// ImageOcclusionShape is one shape of an image-occlusion cloze: its kind
// ("rect", "ellipse", "polygon" or "text") and its attributes.
type ImageOcclusionShape struct {
	Shape      string
	Properties []ImageOcclusionProperty
}

// ImageOcclusion holds every shape that belongs to one cloze number, which is
// how the image-occlusion editor addresses a shape.
type ImageOcclusion struct {
	Ordinal int
	Shapes  []ImageOcclusionShape
}

// parseImageCloze splits "rect:left=0.1:top=0.2" into the shape name and its
// properties. A value may escape ":" and "\" with a backslash, and a shape
// without a ":" separator is not an image occlusion at all.
func parseImageCloze(text string) (string, []ImageOcclusionProperty, bool) {
	shape, _, found := strings.Cut(text, ":")
	if !found {
		return "", nil, false
	}
	var properties []ImageOcclusionProperty
	index := len(shape)
	for index < len(text) && text[index] == ':' {
		nameStart := index + 1
		nameEnd := nameStart
		for nameEnd < len(text) && text[nameEnd] != '=' {
			nameEnd++
		}
		if nameEnd == nameStart || nameEnd == len(text) {
			break
		}
		name := text[nameStart:nameEnd]

		var value strings.Builder
		index = nameEnd + 1
		for index < len(text) {
			c := text[index]
			if c == ':' {
				break
			}
			if c == '\\' {
				if index+1 < len(text) && (text[index+1] == '\\' || text[index+1] == ':') {
					value.WriteByte(text[index+1])
					index += 2
					continue
				}
				break
			}
			value.WriteByte(c)
			index++
		}
		properties = append(properties, ImageOcclusionProperty{Name: name, Value: value.String()})
	}
	return shape, properties, true
}

// imageClozeData turns the shape description into the data attributes the
// image-occlusion client reads. Attributes with an empty (or zero-sized) value
// are dropped, and the trailing space is part of the format because the caller
// appends ">" directly.
func imageClozeData(text string) string {
	shape, properties, ok := parseImageCloze(text)
	if !ok {
		return ""
	}
	var b strings.Builder
	switch shape {
	case "rect", "ellipse", "polygon", "text":
		b.WriteString(`data-shape="` + shape + `" `)
	}
	for _, property := range properties {
		switch property.Name {
		case "left", "top", "angle", "fill":
			if property.Value != "" {
				b.WriteString(`data-` + property.Name + `="` + property.Value + `" `)
			}
		case "width", "height", "rx", "ry", "scale":
			if !emptyOrZero(property.Value) {
				b.WriteString(`data-` + property.Name + `="` + property.Value + `" `)
			}
		case "points":
			if property.Value != "" {
				var points []string
				for _, pair := range strings.Split(property.Value, " ") {
					x, y, found := strings.Cut(pair, ",")
					if !found {
						continue
					}
					points = append(points, x+","+y)
				}
				if len(points) > 0 {
					b.WriteString(`data-points="` + strings.Join(points, " ") + `" `)
				}
			}
		case "oi":
			if property.Value != "" {
				b.WriteString(`data-occludeInactive="` + property.Value + `" `)
			}
		case "text":
			if property.Value != "" {
				b.WriteString(`data-text="` + encodeAttribute(property.Value) + `" `)
			}
		case "fs":
			if property.Value != "" {
				b.WriteString(`data-font-size="` + property.Value + `" `)
			}
		}
	}
	return b.String()
}

func emptyOrZero(value string) bool {
	return value == "" || value == "0"
}

// containsCloze reports whether text holds a cloze deletion numbered above
// zero. A number zero is an image-occlusion placeholder, which is not a card,
// so a field holding only that is not a cloze field.
func containsCloze(text string) bool {
	for _, node := range parseClozes(text) {
		if node.cloze == nil {
			continue
		}
		for _, ordinal := range node.cloze.ordinals {
			if ordinal != 0 {
				return true
			}
		}
	}
	return false
}

// clozeNumbersInFields returns the cloze numbers found in any of the fields, in
// ascending order. Card generation uses it to create one card per number; zero
// is excluded because it names no card.
func clozeNumbersInFields(fields []string) []int {
	numbers := make(map[int]bool)
	for _, field := range fields {
		for ordinal := range clozeNumbersIn(field) {
			numbers[ordinal] = true
		}
	}
	sorted := make([]int, 0, len(numbers))
	for ordinal := range numbers {
		sorted = append(sorted, ordinal)
	}
	sort.Ints(sorted)
	return sorted
}

// extractClozeForTyping returns the text a user has to type for one cloze
// number: the revealed text of every deletion carrying that number, joined
// with ", ". Identical matches collapse to a single entry, and a number that
// occurs nowhere yields the empty string. The `type:cloze` control compares its
// input against this text.
func extractClozeForTyping(text string, ordinal int) string {
	var parts []string
	for _, node := range parseClozes(text) {
		collectClozeText(node, ordinal, false, &parts)
	}
	switch {
	case len(parts) == 0:
		return ""
	case allEqual(parts):
		return parts[len(parts)-1]
	default:
		return strings.Join(parts, ", ")
	}
}

func allEqual(values []string) bool {
	for _, value := range values[1:] {
		if value != values[0] {
			return false
		}
	}
	return true
}

// clozePattern matches one whole cloze deletion, used by stripClozes.
var clozePattern = regexp.MustCompile(`(?s)\{\{c[\d,]+::(.*?)(::.*?)?\}\}`)

// stripClozes removes every cloze marker and its hint, keeping the text. Search
// and sorting use the stripped text so a card matches the words it shows.
func stripClozes(text string) string {
	return clozePattern.ReplaceAllString(text, "$1")
}

// latexPattern matches the three LaTeX forms Anki recognises: a [latex] block,
// an inline [$]...[/$] and a display [$$]...[/$$].
var latexPattern = regexp.MustCompile(`(?is)\[latex\](.+?)\[/latex\]|\[\$\](.+?)\[/\$\]|\[\$\$\](.+?)\[/\$\$\]`)

// containsLatex reports whether text holds a LaTeX element. The text that
// precedes actually generating LaTeX images lives in latex.go; this predicate
// only decides whether the expansion below has work to do.
func containsLatex(text string) bool {
	return latexPattern.MatchString(text)
}

// expandClozesToRevealLatex renders every cloze number twice, hidden and
// revealed, so that LaTeX inside a deletion is extracted for each side. Without
// LaTeX in any deletion it returns the empty string. Concatenating the sides
// reproduces the text every card of the note would show, which is what Anki
// hands to its LaTeX extractor.
func expandClozesToRevealLatex(text string) string {
	if !containsLatex(text) {
		return ""
	}
	var b strings.Builder
	for _, ordinal := range clozeNumbersInFields([]string{text}) {
		b.WriteString(revealClozeText(text, ordinal, true))
		b.WriteString(revealClozeText(text, ordinal, false))
	}
	return b.String()
}

// ParseImageOcclusions returns the shapes of the image-occlusion clozes in
// text, one entry per cloze number, ordered by number. A shape written as
// "image-occlusion:<shape>:<attributes>" can be addressed by several numbers,
// and then it appears under each of them. Nested image occlusions are not
// walked, because Anki only reads the top-level deletions.
func ParseImageOcclusions(text string) []ImageOcclusion {
	shapes := make(map[int][]ImageOcclusionShape)
	for _, node := range parseClozes(text) {
		if node.cloze == nil {
			continue
		}
		shapeText, isImage := node.cloze.imageOcclusionText()
		if !isImage {
			continue
		}
		shape, properties, ok := parseImageCloze(shapeText)
		if !ok {
			continue
		}
		for _, ordinal := range node.cloze.ordinals {
			// Each entry owns its properties so a caller that edits one shape
			// cannot change the shape of another number.
			own := append([]ImageOcclusionProperty(nil), properties...)
			shapes[ordinal] = append(shapes[ordinal], ImageOcclusionShape{Shape: shape, Properties: own})
		}
	}
	ordinals := make([]int, 0, len(shapes))
	for ordinal := range shapes {
		ordinals = append(ordinals, ordinal)
	}
	sort.Ints(ordinals)
	occlusions := make([]ImageOcclusion, len(ordinals))
	for i, ordinal := range ordinals {
		occlusions[i] = ImageOcclusion{Ordinal: ordinal, Shapes: shapes[ordinal]}
	}
	return occlusions
}
