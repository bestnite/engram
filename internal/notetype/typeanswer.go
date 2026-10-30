// Copyright: Ankitects Pty Ltd and contributors
// License: GNU AGPL, version 3 or later; http://www.gnu.org/licenses/agpl.html
//
// Ported from rslib/src/typeanswer.rs at tag 26.09.3 of ankitects/anki.
//
// Anki compares a typed answer with the expected text character by character and
// renders the result as two lines: the typed line marks wrong input and missing
// characters, an arrow separates it from the expected line, and the expected line
// shows what was missed. The comparison runs on NFC text, so a composed and a
// decomposed form of the same letter are equal. The non-combining variant
// ("type-nc") removes combining marks from both sides before comparing, so a
// reader who cannot produce the diacritics is not marked wrong; the expected line
// still shows them.
//
// The sequence matcher is a port of the difflib crate Anki depends on, which is
// itself a port of Python's difflib. It is not taken from a Go difflib library
// because Anki's crate does not behave like Python's for long answers: once the
// expected text reaches 200 characters, the crate keeps only the elements that
// occur more than len/100+1 times and drops every other candidate, where Python
// drops the frequent ones instead. That makes Anki compare two long answers
// almost entirely as one replacement, and a Python-compatible library would
// report a long matching run instead. The port below keeps Anki's rule, so the
// output matches Anki for every input length.

package notetype

import (
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// CompareAnswer returns Anki's HTML for one typed answer: a <code id=typeans>
// block holding the comparison. combining selects the plain comparison; when it
// is false the comparison ignores combining marks, which is the `type-nc` variant.
//
// The expected text is reduced to the text a reader would type - audio and TTS
// tags, line breaks and markup are removed - while the typed text is used as
// entered. An empty typed answer produces the expected text alone.
func CompareAnswer(expected, typed string, combining bool) string {
	stripped := stripTypeAnswerExpected(expected)
	if typed == "" {
		return typeAnswerHTML(encodeMinimal(stripped))
	}
	if combining {
		return newTypeAnswerDiff(stripped, typed).html()
	}
	return newTypeAnswerDiffNonCombining(stripped, typed).html()
}

// ExtractClozeForTyping returns the text a `type:cloze` control compares against
// for one cloze number. field is the stored field value, with its {{cN::...}}
// markers, not rendered HTML; clozeNumber is one-based, so a card with ordinal n
// uses cloze number n+1. A number that occurs in no deletion yields the empty
// string, and identical deletions collapse to a single entry.
func ExtractClozeForTyping(field string, clozeNumber int) string {
	return extractClozeForTyping(field, clozeNumber)
}

// ExtractClozeBlanks returns the text of every deletion carrying clozeNumber,
// in order of appearance, with a repeated number kept as one entry per
// occurrence. ExtractClozeForTyping is its collapsed form: it collapses
// identical deletions to one and joins the rest with ", ", which is the text a
// single type-in box compares against. A card that shows one input per blank
// needs the separate entries, so the grader can compare each answer with its
// own deletion.
func ExtractClozeBlanks(field string, clozeNumber int) []string {
	var parts []string
	for _, node := range parseClozes(field) {
		collectClozeText(node, clozeNumber, false, &parts)
	}
	return parts
}

// typeAnswerLinebreaks matches the markup Anki turns into one space before the
// expected text is stripped, so a line break separates words instead of joining
// them.
var typeAnswerLinebreaks = regexp.MustCompile(`(?is)(\n|<br\s?/?>|</?div>)+`)

// stripTypeAnswerExpected is Anki's strip_expected(): remove audio and TTS tags,
// turn line breaks into spaces, remove the remaining markup, resolve entities and
// trim the ends.
func stripTypeAnswerExpected(expected string) string {
	withoutAV := stripAVTags(expected)
	withoutLinebreaks := typeAnswerLinebreaks.ReplaceAllString(withoutAV, " ")
	return strings.TrimSpace(stripHTML(withoutLinebreaks))
}

// typeAnswerHTML wraps a comparison in Anki's type-in container.
func typeAnswerHTML(content string) string {
	return "<code id=typeans>" + content + "</code>"
}

// diffTokenKind is the meaning of one token in the comparison.
type diffTokenKind int

const (
	tokenGood diffTokenKind = iota
	tokenBad
	tokenMissed
)

// diffToken is one run of characters that the comparison marks the same way.
type diffToken struct {
	kind diffTokenKind
	text string
}

// class is the CSS class Anki gives the token.
func (token diffToken) class() string {
	switch token.kind {
	case tokenBad:
		return "typeBad"
	case tokenMissed:
		return "typeMissed"
	default:
		return "typeGood"
	}
}

// answerDiff is one comparison. typed and expected hold the characters that are
// compared; expectedOriginal is what the expected line shows when the two sides
// match, and split is set by the non-combining variant only, holding the original
// text of each compared character including its combining marks.
type answerDiff struct {
	typed            []rune
	expected         []rune
	expectedOriginal string
	split            []string
}

// newTypeAnswerDiff compares the NFC forms of both sides.
func newTypeAnswerDiff(expected, typed string) *answerDiff {
	return &answerDiff{
		typed:            []rune(norm.NFC.String(typed)),
		expected:         []rune(norm.NFC.String(expected)),
		expectedOriginal: norm.NFC.String(expected),
	}
}

// newTypeAnswerDiffNonCombining compares both sides with combining marks
// removed, while remembering the original text of every remaining character so
// the expected line can still show the marks.
func newTypeAnswerDiffNonCombining(expected, typed string) *answerDiff {
	typedStripped := make([]rune, 0, len(typed))
	for _, character := range norm.NFKD.String(typed) {
		if !isCombiningMark(character) {
			typedStripped = append(typedStripped, character)
		}
	}

	expectedStripped := make([]rune, 0, len(expected))
	split := make([]string, 0, len(expected))
	for _, character := range norm.NFKD.String(expected) {
		if isCombiningMark(character) {
			if len(split) > 0 {
				split[len(split)-1] += string(character)
			}
			continue
		}
		expectedStripped = append(expectedStripped, character)
		split = append(split, string(character))
	}

	return &answerDiff{
		typed:            typedStripped,
		expected:         expectedStripped,
		expectedOriginal: expected,
		split:            split,
	}
}

// html renders the comparison. Equal sides collapse to a single good run, which
// is why a correct answer has no arrow.
func (diff *answerDiff) html() string {
	if slices.Equal(diff.typed, diff.expected) {
		return typeAnswerHTML("<span class=typeGood>" + encodeMinimal(diff.expectedOriginal) + "</span>")
	}
	typedTokens, expectedTokens := diff.tokens()
	return typeAnswerHTML(
		renderDiffTokens(typedTokens) +
			"<br><span id=typearrow>&darr;</span><br>" +
			diff.renderExpectedTokens(expectedTokens),
	)
}

// tokens runs the sequence matcher and turns each opcode into the tokens of both
// lines, as Anki does: a deletion only marks the typed line, an insertion marks
// the missing characters on the typed line as dashes and on the expected line as
// the characters themselves, and a replacement marks both.
func (diff *answerDiff) tokens() ([]diffToken, []diffToken) {
	matcher := newAnswerMatcher(diff.typed, diff.expected)
	var typedTokens, expectedTokens []diffToken
	for _, opcode := range matcher.opcodes() {
		typedSlice := string(diff.typed[opcode.firstStart:opcode.firstEnd])
		expectedSlice := string(diff.expected[opcode.secondStart:opcode.secondEnd])
		switch opcode.tag {
		case 'e':
			typedTokens = append(typedTokens, diffToken{tokenGood, typedSlice})
			expectedTokens = append(expectedTokens, diffToken{tokenGood, expectedSlice})
		case 'd':
			typedTokens = append(typedTokens, diffToken{tokenBad, typedSlice})
		case 'i':
			typedTokens = append(typedTokens, diffToken{tokenMissed, strings.Repeat("-", utf8.RuneCountInString(expectedSlice))})
			expectedTokens = append(expectedTokens, diffToken{tokenMissed, expectedSlice})
		case 'r':
			typedTokens = append(typedTokens, diffToken{tokenBad, typedSlice})
			expectedTokens = append(expectedTokens, diffToken{tokenMissed, expectedSlice})
		}
	}
	return typedTokens, expectedTokens
}

// renderExpectedTokens renders the expected line. The non-combining variant puts
// the original text back, because its tokens hold only the comparing characters.
func (diff *answerDiff) renderExpectedTokens(tokens []diffToken) string {
	if diff.split == nil {
		return renderDiffTokens(tokens)
	}
	var rendered strings.Builder
	index := 0
	for _, token := range tokens {
		end := index + utf8.RuneCountInString(token.text)
		text := strings.Join(diff.split[index:end], "")
		index = end
		rendered.WriteString("<span class=" + token.class() + ">" + encodeMinimal(text) + "</span>")
	}
	return rendered.String()
}

// renderDiffTokens renders one line of tokens. A token that starts with a
// combining mark is prefixed with a non-breaking space so the mark cannot join
// the previous token's last character.
func renderDiffTokens(tokens []diffToken) string {
	var rendered strings.Builder
	for _, token := range tokens {
		rendered.WriteString("<span class=" + token.class() + ">" + encodeMinimal(isolateLeadingMark(token.text)) + "</span>")
	}
	return rendered.String()
}

// isolateLeadingMark prefixes a leading combining mark with a non-breaking
// space. The mark alone would otherwise attach to the preceding character, which
// changes what the reader sees.
func isolateLeadingMark(text string) string {
	if text == "" {
		return text
	}
	first, _ := utf8.DecodeRuneInString(text)
	if isCombiningMark(first) {
		return "\u00a0" + text
	}
	return text
}

// isCombiningMark reports whether the character is a combining mark: the Mn, Mc
// and Me general categories, which is the set Anki's normalisation library calls
// a combining mark.
func isCombiningMark(character rune) bool {
	return unicode.In(character, unicode.Mn, unicode.Mc, unicode.Me)
}

// answerMatch is one matching block: a run of equal characters in both
// sequences.
type answerMatch struct {
	firstStart  int
	secondStart int
	size        int
}

// answerOpcode is one edit of the comparison. tag is Anki's single-letter tag:
// 'e' for equal, 'd' for delete, 'i' for insert and 'r' for replace.
type answerOpcode struct {
	tag                    byte
	firstStart, firstEnd   int
	secondStart, secondEnd int
}

// answerMatcher finds the matching blocks between two rune sequences the way
// Anki's matcher does.
type answerMatcher struct {
	first  []rune
	second []rune
	// candidates holds the positions in second of every element the matcher is
	// willing to match on.
	candidates map[rune][]int
}

// newAnswerMatcher indexes the expected sequence. Anki's matcher drops every
// element that occurs at most len/100+1 times once the expected sequence has
// 200 characters, so only often-repeated characters can anchor a match on a long
// answer. That is what Anki ships, and it is reproduced here rather than
// corrected, because the comparison output must match Anki's.
func newAnswerMatcher(typed, expected []rune) *answerMatcher {
	matcher := &answerMatcher{
		first:      typed,
		second:     expected,
		candidates: make(map[rune][]int),
	}
	for index, character := range expected {
		matcher.candidates[character] = append(matcher.candidates[character], index)
	}
	if len(expected) >= 200 {
		frequent := len(expected)/100 + 1
		for character, positions := range matcher.candidates {
			if len(positions) <= frequent {
				delete(matcher.candidates, character)
			}
		}
	}
	return matcher
}

// findLongestMatch returns the first longest matching block inside the given
// ranges, extending it as far as the surrounding characters allow.
func (matcher *answerMatcher) findLongestMatch(firstStart, firstEnd, secondStart, secondEnd int) answerMatch {
	bestFirst, bestSecond, bestSize := firstStart, secondStart, 0
	previous := make(map[int]int)
	for index := firstStart; index < firstEnd; index++ {
		current := make(map[int]int)
		for _, secondIndex := range matcher.candidates[matcher.first[index]] {
			if secondIndex < secondStart {
				continue
			}
			if secondIndex >= secondEnd {
				break
			}
			size := 0
			if secondIndex > 0 {
				size = previous[secondIndex-1]
			}
			size++
			current[secondIndex] = size
			if size > bestSize {
				bestFirst = index + 1 - size
				bestSecond = secondIndex + 1 - size
				bestSize = size
			}
		}
		previous = current
	}
	for bestFirst > firstStart && bestSecond > secondStart && matcher.first[bestFirst-1] == matcher.second[bestSecond-1] {
		bestFirst--
		bestSecond--
		bestSize++
	}
	for bestFirst+bestSize < firstEnd && bestSecond+bestSize < secondEnd && matcher.first[bestFirst+bestSize] == matcher.second[bestSecond+bestSize] {
		bestSize++
	}
	return answerMatch{firstStart: bestFirst, secondStart: bestSecond, size: bestSize}
}

// matchingBlocks splits both sequences recursively around their longest common
// runs, then merges runs that touch and appends the sentinel block Anki uses to
// close the list.
func (matcher *answerMatcher) matchingBlocks() []answerMatch {
	type rangeBlock struct {
		firstStart, firstEnd   int
		secondStart, secondEnd int
	}
	queue := []rangeBlock{{0, len(matcher.first), 0, len(matcher.second)}}
	matches := []answerMatch{}
	for len(queue) > 0 {
		current := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		match := matcher.findLongestMatch(current.firstStart, current.firstEnd, current.secondStart, current.secondEnd)
		if match.size == 0 {
			continue
		}
		if current.firstStart < match.firstStart && current.secondStart < match.secondStart {
			queue = append(queue, rangeBlock{current.firstStart, match.firstStart, current.secondStart, match.secondStart})
		}
		if match.firstStart+match.size < current.firstEnd && match.secondStart+match.size < current.secondEnd {
			queue = append(queue, rangeBlock{match.firstStart + match.size, current.firstEnd, match.secondStart + match.size, current.secondEnd})
		}
		matches = append(matches, match)
	}
	sort.Slice(matches, func(left, right int) bool {
		if matches[left].firstStart != matches[right].firstStart {
			return matches[left].firstStart < matches[right].firstStart
		}
		if matches[left].secondStart != matches[right].secondStart {
			return matches[left].secondStart < matches[right].secondStart
		}
		return matches[left].size < matches[right].size
	})

	merged := make([]answerMatch, 0, len(matches)+1)
	firstStart, secondStart, size := 0, 0, 0
	for _, match := range matches {
		if firstStart+size == match.firstStart && secondStart+size == match.secondStart {
			size += match.size
			continue
		}
		if size != 0 {
			merged = append(merged, answerMatch{firstStart: firstStart, secondStart: secondStart, size: size})
		}
		firstStart, secondStart, size = match.firstStart, match.secondStart, match.size
	}
	if size != 0 {
		merged = append(merged, answerMatch{firstStart: firstStart, secondStart: secondStart, size: size})
	}
	return append(merged, answerMatch{firstStart: len(matcher.first), secondStart: len(matcher.second), size: 0})
}

// opcodes turns the matching blocks into the edits between them, in the order
// Anki applies them.
func (matcher *answerMatcher) opcodes() []answerOpcode {
	var opcodes []answerOpcode
	first, second := 0, 0
	for _, match := range matcher.matchingBlocks() {
		var tag byte
		switch {
		case first < match.firstStart && second < match.secondStart:
			tag = 'r'
		case first < match.firstStart:
			tag = 'd'
		case second < match.secondStart:
			tag = 'i'
		}
		if tag != 0 {
			opcodes = append(opcodes, answerOpcode{tag, first, match.firstStart, second, match.secondStart})
		}
		first = match.firstStart + match.size
		second = match.secondStart + match.size
		if match.size != 0 {
			opcodes = append(opcodes, answerOpcode{'e', match.firstStart, first, match.secondStart, second})
		}
	}
	return opcodes
}

// minimalEntities escapes the five characters Anki's minimal encoder escapes.
// It is not encodeAttribute: an attribute value is hex-escaped, while text keeps
// every other character as it is.
var minimalEntities = strings.NewReplacer(
	"&", "&amp;",
	"'", "&#x27;",
	`"`, "&quot;",
	"<", "&lt;",
	">", "&gt;",
)

// encodeMinimal escapes text for HTML content the way Anki does.
func encodeMinimal(text string) string {
	return minimalEntities.Replace(text)
}
