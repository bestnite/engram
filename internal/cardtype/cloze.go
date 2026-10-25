package cardtype

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"git.nite07.com/nite/engram/internal/texmath"
)

// ClozeDeletion is one cloze: {{cN::text}} or {{cN::text::hint}}.
type ClozeDeletion struct {
	Index int    // The N in cN, the source of the card identity cloze:<index>.
	Text  string // The cloze content verbatim.
	Hint  string // Optional hint; an empty string when there is none.
	Start int    // Start byte offset, pointing at "{{".
	End   int    // End byte offset, right after the closing "}}".
	// InMath says the whole cloze falls inside a formula: the front and back must highlight in TeX, and a pipe inside the content also belongs to TeX.
	// A pipe therefore never separates answers here, so several accepted answers in a formula are written with \alt instead.
	InMath bool
}

// ParseCloze parses cloze text and returns the outermost cloze items in order of first appearance.
//
// Syntax and edge cases:
//   - {{cN::text}} and {{cN::text::hint}}; N is one or more decimal digits.
//   - Paired {{ ... }} inside the content match by depth and never close early (nested braces).
//     A {{cM::...}} inside the nesting is kept verbatim as ordinary content and does not become its own card — only the outermost is parsed.
//   - \{ \} \\ are escapes: an escaped character takes no part in structural parsing but is preserved verbatim (TeX-safe,
//     e.g. the backslash in \frac is unaffected).
//   - A single "{" directly before a cloze belongs to the outer text (TeX grouping): "{{{c1::x}}}" parses as
//     "{" + cloze x + "}".
//   - An unclosed cloze item or one with empty content returns a readable English error.
//   - Several acceptable answers are separated by a top-level \alt, or, when the cloze is not inside a formula, by a top-level | (see splitAlternatives).
//     Separators are not split at parse time: they stay verbatim in Text.
//
// With no cloze items it returns an empty slice and a nil error, leaving it to the caller (Validate) to decide how to report that.
func ParseCloze(text string) ([]ClozeDeletion, error) {
	var out []ClozeDeletion
	math := texmath.Ranges(text)
	i := 0
	for i < len(text) {
		if escaped, next := skipEscape(text, i); escaped {
			i = next
			continue
		}
		if !strings.HasPrefix(text[i:], "{{") {
			i++
			continue
		}
		index, contentStart, ok := parseClozeOpener(text, i)
		if !ok {
			// Not a cloze marker: advance only one byte. In TeX a cloze often follows a grouping brace closely
			// (\dfrac{a}{{{c1::b}}}); in "{{", the real cloze starts at the second "{";
			// skipping two bytes at once would miss it.
			i++
			continue
		}
		del, end, err := scanClozeContent(text, i, contentStart, index)
		if err != nil {
			return nil, err
		}
		del.InMath = texmath.Contains(math, del.Start, del.End)
		out = append(out, del)
		i = end
	}
	return out, nil
}

// skipEscape handles the three escapes \{ \} \\: it returns whether one matched, and the position after skipping.
// It only applies to these three characters, so backslashes such as \frac in TeX are not broken.
func skipEscape(s string, i int) (bool, int) {
	if s[i] != '\\' || i+1 >= len(s) {
		return false, i
	}
	switch s[i+1] {
	case '{', '}', '\\':
		return true, i + 2
	}
	return false, i
}

// parseClozeOpener reports whether s[i:] starts with {{cN::; if so it returns the index and the content start.
func parseClozeOpener(s string, i int) (index, contentStart int, ok bool) {
	j := i + 2
	if j >= len(s) || s[j] != 'c' {
		return 0, 0, false
	}
	j++
	digitStart := j
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == digitStart {
		return 0, 0, false
	}
	if !strings.HasPrefix(s[j:], "::") {
		return 0, 0, false
	}
	n, err := strconv.Atoi(s[digitStart:j])
	if err != nil {
		// Number too large to represent: treat it as plain text rather than failing the parse.
		return 0, 0, false
	}
	return n, j + 2, true
}

// scanClozeContent scans from the content start to the outermost "}}", returning the cloze item and the end position.
// depth tracks nested {{ }}; the first top-level "::" in the content separates text from hint.
func scanClozeContent(s string, start, contentStart, index int) (ClozeDeletion, int, error) {
	depth := 0
	hintSep := -1
	j := contentStart
	for j < len(s) {
		if escaped, next := skipEscape(s, j); escaped {
			j = next
			continue
		}
		switch {
		case strings.HasPrefix(s[j:], "{{"):
			depth++
			j += 2
		case strings.HasPrefix(s[j:], "}}"):
			if depth > 0 {
				depth--
				j += 2
				continue
			}
			text, hint := splitClozeContent(s[contentStart:j], hintSep, contentStart)
			if text == "" {
				return ClozeDeletion{}, 0, fmt.Errorf("cloze: cloze deletion c%d has empty text", index)
			}
			return ClozeDeletion{Index: index, Text: text, Hint: hint, Start: start, End: j + 2}, j + 2, nil
		case depth == 0 && hintSep < 0 && strings.HasPrefix(s[j:], "::"):
			hintSep = j
			j += 2
		default:
			j++
		}
	}
	return ClozeDeletion{}, 0, fmt.Errorf("cloze: unclosed cloze deletion starting at offset %d", start)
}

// splitClozeContent splits the content into text and hint at the top-level "::" (hintSep of -1 means no hint).
func splitClozeContent(content string, hintSep, contentStart int) (text, hint string) {
	if hintSep < 0 {
		return content, ""
	}
	rel := hintSep - contentStart
	return content[:rel], content[rel+2:]
}

// clozeType is the cloze card: each distinct index produces one card.
// Fields: text (required, containing at least one {{cN::…}}), extra / source_url (optional).
type clozeType struct{}

// Label returns the language pack key.
func (clozeType) Label() string { return "cardtype.cloze" }

// Describe self-describes: both sides read text; the answer control is per-blank input (one input box per target cloze).
// PromptField is deliberately left empty: the raw text already contains the answers, so the prompt can only be the server-side masked front,
// and when the front has not been fetched the front end must not fall back to the raw field text.
func (clozeType) Describe() Description {
	return Description{
		Kind:          "cloze",
		LabelKey:      "cardtype.cloze",
		AnswerControl: AnswerBlanks,
		FrontField:    "text",
		BackField:     "text",
		Fields: fieldsWithCommon(
			FieldSpec{Key: "text", Control: ControlTextarea, Required: true},
		),
		Example: map[string]any{
			"text": "{{c1::Paris}} is the capital of {{c2::France|French Republic::country}}; the derivative of \\(x^2\\) is {{c3::\\(2x\\)}}.",
		},
	}
}

// Validate checks text and requires at least one cloze item.
func (clozeType) Validate(fields map[string]any) error {
	text, err := stringField(fields, "text")
	if err != nil {
		return fmt.Errorf("cloze: %w", err)
	}
	dels, err := ParseCloze(text)
	if err != nil {
		return err
	}
	if len(dels) == 0 {
		// The schema pattern can only express {{cN::, so give a clearer, readable error here.
		return errors.New(`cloze: field "text" must contain at least one cloze deletion like {{c1::text}}`)
	}
	for _, d := range dels {
		if !d.InMath && altInsideFormula(d.Text) {
			return fmt.Errorf(`cloze: cloze deletion c%d has \alt inside a formula; write the alternatives outside the formula, each wrapped in its own formula, for example {{c1::\(a\)|\(b\)}}`, d.Index)
		}
		for _, alt := range splitAlternatives(d.Text, d.InMath) {
			if strings.TrimSpace(alt) == "" {
				return fmt.Errorf("cloze: cloze deletion c%d has an empty answer alternative around \"|\" or \"\\alt\"", d.Index)
			}
		}
	}
	if err := validateCommonOptional(fields); err != nil {
		return fmt.Errorf("cloze: %w", err)
	}
	return nil
}

// Cards produces one card per index in order of first appearance; a repeated index produces only one card.
func (clozeType) Cards(note Note) []Card {
	text, _ := note.Fields["text"].(string)
	dels, _ := ParseCloze(text)
	seen := make(map[int]bool, len(dels))
	cards := make([]Card, 0, len(dels))
	ordinal := 0
	for _, d := range dels {
		if seen[d.Index] {
			continue
		}
		seen[d.Index] = true
		cards = append(cards, Card{
			Template: fmt.Sprintf("cloze:%d", d.Index),
			Ordinal:  ordinal,
			Fields:   note.Fields,
		})
		ordinal++
	}
	return cards
}

// Render masks the target index on the front and reveals and highlights it on the back; other indices show the cloze content (without the {{cN::}} marker).
func (clozeType) Render(card Card, side Side) (RenderResult, error) {
	index, ok := parseClozeTemplate(card.Template)
	if !ok {
		return RenderResult{}, errUnknownTemplate(card.Template)
	}
	text, _ := card.Fields["text"].(string)
	dels, err := ParseCloze(text)
	if err != nil {
		return RenderResult{}, err
	}
	return RenderResult{Body: renderCloze(text, dels, index, side == SideBack)}, nil
}

// parseClozeTemplate parses a template of the form "cloze:<index>".
func parseClozeTemplate(template string) (int, bool) {
	const prefix = "cloze:"
	if !strings.HasPrefix(template, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(template[len(prefix):])
	if err != nil {
		return 0, false
	}
	return n, true
}

// renderCloze rebuilds the text by byte offset, keeping the Markdown/TeX source outside the clozes character for character.
//
// The cloze markers themselves never appear on the card face: the target index becomes a placeholder on the front and the content on the back, while other indices show only their content.
// Highlighting of the target index takes one of two forms depending on position:
//   - Outside a formula, wrap it in <span class="cloze">. span[class] is on the render package's allowlist, so
//     raw inline HTML passes through goldmark as-is and Markdown inside the content renders as usual.
//   - Inside a formula, write \boxed{…} to box the tested point. The render package protects the whole formula as plain text and escapes
//     < > &, so a span inside a formula only becomes literal angle brackets; and a formula cannot be cut into pieces at the cloze either, or
//     grouping such as \dfrac{…}{…} would break — which is why the highlight must be expressed in TeX itself.
//     Only macros already in the self-hosted MathJax bundle may be used: \boxed comes with the bundled ams, \text with base.
//     \class, \color and \bbox all need an extra extension file loaded at runtime, while the static assets only host the main bundle,
//     and a failed load aborts typesetting for the whole container, so they cannot be used.
//
// A cloze that only half enters a formula is treated as outside it: neither form works, so the original span is kept.
func renderCloze(text string, dels []ClozeDeletion, target int, reveal bool) string {
	var b strings.Builder
	last := 0
	for _, d := range dels {
		if d.Start < last {
			// Defensive: only the outermost is parsed, so offsets never overlap; should they overlap anyway, skip to avoid double writes.
			continue
		}
		b.WriteString(text[last:d.Start])
		alts := clozeAlternatives(d.Text, d.InMath)
		switch {
		case d.Index != target:
			// An untested cloze is context, so give only the canonical wording, so the sentence still reads.
			b.WriteString(stripCloze(alts[0]))
		case reveal && d.InMath:
			// Every acceptable answer is boxed as one unit: \alt separates answers that are all correct, so the back has to show all of them.
			// The spaces around the separator are trimmed so the joined TeX keeps the single spacing of the separator itself.
			b.WriteString(`\boxed{` + strings.Join(trimEach(stripEach(alts)), `\ /\ `) + `}`)
		case reveal:
			// When revealing the tested point, list every acceptable wording so the user can see which phrasings all count.
			b.WriteString(`<span class="cloze">` + strings.Join(stripEach(alts), " / ") + `</span>`)
		case d.InMath:
			// The placeholder is text rather than TeX, so typeset it with \text; the hint goes verbatim into \text, so its braces must be paired.
			b.WriteString(`\boxed{\text{` + clozeBlank(d) + `}}`)
		default:
			b.WriteString(`<span class="cloze">` + clozeBlank(d) + `</span>`)
		}
		last = d.End
	}
	b.WriteString(text[last:])
	return b.String()
}

// stripCloze replaces a nested {{cM::…}} in the content with its content: nested markers do not become their own card,
// but they must not appear on the card face in raw syntax either. On parse failure it returns the text verbatim (the outer level was already validated, so this should not happen).
func stripCloze(text string) string {
	dels, err := ParseCloze(text)
	if err != nil || len(dels) == 0 {
		return text
	}
	return renderCloze(text, dels, -1, true)
}

// clozeBlank builds the front-side placeholder: [hint] when there is a hint, otherwise […].
func clozeBlank(d ClozeDeletion) string {
	if d.Hint != "" {
		return "[" + d.Hint + "]"
	}
	return "[…]"
}

// ClozeInput is the input of clozeType.Grade: Answers correspond, in order of appearance, to each cloze of this card's target index.
type ClozeInput struct {
	GradeContext
	Answers []string
}

// ParseAnswer decodes the answer: a string array answering each blank in order of appearance; no answer is equivalent to leaving every blank empty (graded all wrong).
func (clozeType) ParseAnswer(gc GradeContext, raw json.RawMessage) (any, error) {
	answers := []string{}
	if !emptyAnswer(raw) {
		if err := json.Unmarshal(raw, &answers); err != nil {
			return nil, errors.New("cloze answer must be an array of strings, one per blank")
		}
	}
	return ClozeInput{GradeContext: gc, Answers: answers}, nil
}

// GivenText returns the per-blank answers joined by commas; an empty blank still takes a slot, so nothing shifts.
func (clozeType) GivenText(_ map[string]any, detail map[string]any) string {
	given, _ := detail["given"].([]string)
	return strings.Join(given, ", ")
}

// Grade compares blank by blank and scores proportionally: score = blanks answered correctly / total target clozes.
// When one blank has several acceptable answers (separated by \alt, or by | outside a formula), matching any one counts as correct.
// Comparison ignores case and collapses whitespace; when the answer as a whole is an inline formula \( … \), the form without the delimiters also counts,
// because a typeset formula cannot be typed into an input box. More answers than blanks counts as ungradable (answers out of step with the card face);
// with fewer answers than blanks, the missing blanks count as unanswered.
func (clozeType) Grade(input any) (int, map[string]any, bool) {
	in, ok := input.(ClozeInput)
	if !ok {
		return 0, nil, false
	}
	targets, ok := clozeTargets(in.Fields, in.Template)
	if !ok || len(in.Answers) > len(targets) {
		return 0, nil, false
	}
	given := make([]string, len(targets))
	copy(given, in.Answers)
	expected := make([][]string, len(targets))
	correct := make([]bool, len(targets))
	hits := 0
	for i, d := range targets {
		expected[i] = stripEach(clozeAlternatives(d.Text, d.InMath))
		if matchesAlternatives(given[i], expected[i]) {
			correct[i] = true
			hits++
		}
	}
	detail := map[string]any{"answers": expected, "given": given, "correct": correct}
	return in.rating(float64(hits)/float64(len(targets)), detail)
}

// BlankHints returns the hint for each target cloze of this card in order of appearance (empty string when there is none), which the review page uses to give one input box per blank.
func (clozeType) BlankHints(card Card) []string {
	targets, ok := clozeTargets(card.Fields, card.Template)
	if !ok {
		return nil
	}
	hints := make([]string, len(targets))
	for i, d := range targets {
		hints[i] = d.Hint
	}
	return hints
}

// clozeTargets returns every cloze in text corresponding to the template cloze:<index> (in order of appearance);
// it returns false when the template is invalid, parsing fails, or that index does not exist.
func clozeTargets(fields map[string]any, template string) ([]ClozeDeletion, bool) {
	index, ok := parseClozeTemplate(template)
	if !ok {
		return nil, false
	}
	text, _ := fields["text"].(string)
	dels, err := ParseCloze(text)
	if err != nil {
		return nil, false
	}
	var out []ClozeDeletion
	for _, d := range dels {
		if d.Index == index {
			out = append(out, d)
		}
	}
	return out, len(out) > 0
}

// matchesAlternatives reports whether the answer matches any acceptable wording (shared by cloze and list cards):
// it ignores case and collapses whitespace, compares an escaped \| as a literal pipe, and when the wording as a whole is an inline formula \( … \),
// an answer without the delimiters also counts, because a typeset formula cannot be typed into an input box. An empty answer is never correct.
func matchesAlternatives(given string, alts []string) bool {
	for _, alt := range alts {
		if matchesAnswer(given, unescapePipes(alt)) {
			return true
		}
	}
	return false
}

// matchesAnswer reports whether the answer equals one wording (rules in matchesAlternatives).
func matchesAnswer(given, expected string) bool {
	g := normalizeTyped(given, true, true)
	if g == "" {
		return false
	}
	if g == normalizeTyped(expected, true, true) {
		return true
	}
	inner := strings.TrimSpace(expected)
	if len(inner) < 4 || !strings.HasPrefix(inner, `\(`) || !strings.HasSuffix(inner, `\)`) {
		return false
	}
	return g == normalizeTyped(inner[2:len(inner)-2], true, true)
}

// stripEach removes nested cloze markers from each alternative answer.
func stripEach(alts []string) []string {
	out := make([]string, len(alts))
	for i, alt := range alts {
		out[i] = stripCloze(alt)
	}
	return out
}

// trimEach trims the surrounding whitespace of each alternative answer.
func trimEach(alts []string) []string {
	out := make([]string, len(alts))
	for i, alt := range alts {
		out[i] = strings.TrimSpace(alt)
	}
	return out
}

// clozeAlternatives returns all acceptable answers for one cloze, the first being the canonical wording; empty alternatives are dropped,
// and when they are all empty it falls back to the raw text (such wording would not pass Validate; this only guards against old data).
func clozeAlternatives(text string, inMath bool) []string {
	var out []string
	for _, alt := range splitAlternatives(text, inMath) {
		if strings.TrimSpace(alt) != "" {
			out = append(out, alt)
		}
	}
	if len(out) == 0 {
		return []string{text}
	}
	return out
}

// splitAlternatives splits the cloze content at top-level separators, keeping empty segments so Validate can report them.
//
// Two separators exist:
//   - \alt separates answers in a math cloze, whose whole content is a TeX fragment, and at the top level of a plain-text cloze.
//     It is not a TeX macro, so it cannot appear in a legitimate formula, and a math cloze needs a separator because a pipe there is TeX.
//   - | separates answers only outside a formula, and only when the whole cloze is not inside one: \(|x|\) is absolute value.
//
// Neither separator is split on in these positions:
//   - Inside a formula that is part of a plain-text cloze. Splitting there would cut the formula delimiters apart, so Validate
//     rejects a \alt in that position and asks for the alternatives to be written outside the formula.
//   - Inside a nested {{ }}, where it belongs to the nested content.
//   - An escaped \|: use it when writing a literal pipe in prose; Markdown renders \| as |.
//   - A \alt followed by an ASCII letter, which is a different TeX command such as \alpha.
func splitAlternatives(text string, inMath bool) []string {
	math := texmath.Ranges(text)
	var parts []string
	depth, last, i := 0, 0, 0
	for i < len(text) {
		// \alt separates answers in a math cloze, whose content is a TeX fragment, and at the top level of a plain-text cloze.
		// Inside a formula that is part of a plain-text cloze it cannot: splitting there would cut the formula delimiters apart.
		if depth == 0 && !insideMath(math, i) && hasAltSeparator(text, i) {
			parts = append(parts, text[last:i])
			i += len(altSeparator)
			last = i
			continue
		}
		if insideMath(math, i) {
			// Inside a formula a pipe is TeX, and braces or escapes there are TeX, not cloze syntax.
			i++
			continue
		}
		if text[i] == '\\' && i+1 < len(text) && strings.IndexByte(`{}\|`, text[i+1]) >= 0 {
			i += 2
			continue
		}
		switch {
		case strings.HasPrefix(text[i:], "{{"):
			depth++
			i += 2
		case strings.HasPrefix(text[i:], "}}") && depth > 0:
			depth--
			i += 2
		case text[i] == '|' && depth == 0 && !inMath:
			parts = append(parts, text[last:i])
			i++
			last = i
		default:
			i++
		}
	}
	return append(parts, text[last:])
}

// altSeparator separates acceptable answers in a math cloze and at the top level of a plain-text cloze.
// It is not a TeX macro, so no legitimate formula contains it, which is what lets a math cloze use it where a pipe would be TeX.
const altSeparator = `\alt`

// hasAltSeparator reports whether s[i:] starts with the answer separator \alt.
// A following ASCII letter makes it a different control word (\alpha, \aligned), which is not a separator.
func hasAltSeparator(s string, i int) bool {
	if !strings.HasPrefix(s[i:], altSeparator) {
		return false
	}
	j := i + len(altSeparator)
	if j >= len(s) {
		return true
	}
	c := s[j]
	return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z')
}

// altInsideFormula reports whether text has a top-level \alt inside a formula of a plain-text cloze.
// Splitting there would cut the formula delimiters apart, so Validate rejects it instead of producing broken alternatives.
func altInsideFormula(text string) bool {
	math := texmath.Ranges(text)
	depth := 0
	for i := 0; i < len(text); {
		if depth == 0 && insideMath(math, i) && hasAltSeparator(text, i) {
			return true
		}
		if text[i] == '\\' && i+1 < len(text) && strings.IndexByte(`{}\|`, text[i+1]) >= 0 {
			i += 2
			continue
		}
		switch {
		case strings.HasPrefix(text[i:], "{{"):
			depth++
			i += 2
		case strings.HasPrefix(text[i:], "}}") && depth > 0:
			depth--
			i += 2
		default:
			i++
		}
	}
	return false
}

// insideMath reports whether byte position i lies inside one of the formula ranges.
func insideMath(ranges [][]int, i int) bool {
	for _, r := range ranges {
		if r[0] <= i && i < r[1] {
			return true
		}
	}
	return false
}

// mathRangeAt reports whether position i starts a formula, and if so returns the end position of that formula.
func mathRangeAt(ranges [][]int, i int) (int, bool) {
	for _, r := range ranges {
		if r[0] == i {
			return r[1], true
		}
	}
	return 0, false
}

// unescapePipes turns \| outside formulas back into |, for grading comparison: the user types a literal pipe and does not type an escape.
// A \| inside a formula is TeX's double-vertical-bar command and is left as-is.
func unescapePipes(s string) string {
	if !strings.Contains(s, `\|`) {
		return s
	}
	math := texmath.Ranges(s)
	var b strings.Builder
	i := 0
	for i < len(s) {
		if end, ok := mathRangeAt(math, i); ok {
			b.WriteString(s[i:end])
			i = end
			continue
		}
		if strings.HasPrefix(s[i:], `\|`) {
			b.WriteByte('|')
			i += 2
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
