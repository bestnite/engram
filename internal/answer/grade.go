package answer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// The four FSRS rating levels, consistent with the integer convention of
// reviews.rating.
const (
	RatingAgain = 1
	RatingHard  = 2
	RatingGood  = 3
	RatingEasy  = 4
)

// GradeMapping maps a score in [0,1] to a rating level. It mirrors the JSON
// stored in preset.grade_mapping, so the caller can unmarshal the preset value
// into this type and pass it in GradeContext.Mapping. A zero value falls back
// to the defaults field by field.
type GradeMapping struct {
	Version       int     `json:"version"`
	Full          int     `json:"full"`
	Partial       int     `json:"partial"`
	None          int     `json:"none"`
	FullThreshold float64 `json:"full_threshold,omitempty"`
	NoneThreshold float64 `json:"none_threshold,omitempty"`
}

// DefaultGradeMapping returns the default mapping: all correct is Good, a
// partial score is Hard, and all wrong is Again.
func DefaultGradeMapping() GradeMapping {
	return GradeMapping{
		Version:       1,
		Full:          RatingGood,
		Partial:       RatingHard,
		None:          RatingAgain,
		FullThreshold: 1,
		NoneThreshold: 0,
	}
}

// withDefaults fills every unset field with its default, so a caller that
// leaves a field out gets the documented behaviour instead of a zero rating.
// ParseGradeMapping decodes the mapping stored in a preset. An empty string
// (the column is NULL) falls back to the default mapping; this is the only
// entry point for the "missing means default" rule, so every reader agrees.
func ParseGradeMapping(raw string) (GradeMapping, error) {
	mapping := DefaultGradeMapping()
	if raw == "" {
		return mapping, nil
	}
	if err := json.Unmarshal([]byte(raw), &mapping); err != nil {
		return GradeMapping{}, fmt.Errorf("grade mapping: invalid JSON: %w", err)
	}
	if err := mapping.validate(); err != nil {
		return GradeMapping{}, err
	}
	return mapping.withDefaults(), nil
}

// MarshalGradeMapping encodes a mapping for storage in a preset, filling in
// every unset field first so the stored value is complete.
func MarshalGradeMapping(mapping GradeMapping) (string, error) {
	if err := mapping.validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(mapping.withDefaults())
	if err != nil {
		return "", fmt.Errorf("grade mapping: marshal: %w", err)
	}
	return string(raw), nil
}

// GradeMappingVersion is the structure version this package understands.
const GradeMappingVersion = 1

// validate checks the version, the rating range and the threshold ordering.
// Errors name the offending value so a preset edit can show it.
func (m GradeMapping) validate() error {
	if m.Version != 0 && m.Version != GradeMappingVersion {
		return fmt.Errorf("grade mapping: unsupported version %d", m.Version)
	}
	resolved := m.withDefaults()
	for _, entry := range []struct {
		name   string
		rating int
	}{{"full", resolved.Full}, {"partial", resolved.Partial}, {"none", resolved.None}} {
		if entry.rating < RatingAgain || entry.rating > RatingEasy {
			return fmt.Errorf("grade mapping: %s rating %d out of range 1-4", entry.name, entry.rating)
		}
	}
	if resolved.FullThreshold <= resolved.NoneThreshold {
		return fmt.Errorf("grade mapping: full_threshold (%g) must be greater than none_threshold (%g)",
			resolved.FullThreshold, resolved.NoneThreshold)
	}
	return nil
}

func (m GradeMapping) withDefaults() GradeMapping {
	defaults := DefaultGradeMapping()
	if m.Version == 0 {
		m.Version = defaults.Version
	}
	if m.Full == 0 {
		m.Full = defaults.Full
	}
	if m.Partial == 0 {
		m.Partial = defaults.Partial
	}
	if m.None == 0 {
		m.None = defaults.None
	}
	if m.FullThreshold <= 0 {
		m.FullThreshold = defaults.FullThreshold
	}
	if m.NoneThreshold <= 0 {
		m.NoneThreshold = defaults.NoneThreshold
	}
	return m
}

// RatingFor maps a score in [0,1] to a rating level, clamping an out-of-range
// score to [0,1].
func (m GradeMapping) RatingFor(score float64) int {
	resolved := m.withDefaults()
	if math.IsNaN(score) {
		return resolved.None
	}
	score = math.Max(0, math.Min(1, score))
	switch {
	case score >= resolved.FullThreshold:
		return resolved.Full
	case score <= resolved.NoneThreshold:
		return resolved.None
	default:
		return resolved.Partial
	}
}

// GradeResult is one grading pass. Detail is written verbatim to
// reviews.grade_detail_json and carries the same keys as the card kinds this
// package replaces. Given is the display form of the learner's answer.
type GradeResult struct {
	Score  float64
	Rating int
	Detail map[string]any
	Given  string
}

// ErrInvalidAnswer means the submitted JSON does not decode into the shape the
// control expects; callers map it to a bad-request response.
var ErrInvalidAnswer = errors.New("answer: invalid answer")

// ErrUngradable means the answer decodes but cannot be graded against this
// card, for example an option index out of range.
var ErrUngradable = errors.New("answer: answer cannot be graded")

// finish maps the score through ctx.Mapping and fills the common detail keys.
func finish(ctx GradeContext, score float64, detail map[string]any) GradeResult {
	mapping := DefaultGradeMapping()
	if ctx.Mapping != nil {
		mapping = *ctx.Mapping
	}
	rating := mapping.RatingFor(score)
	if detail == nil {
		detail = map[string]any{}
	}
	detail["score"] = score
	detail["rating"] = rating
	return GradeResult{Score: score, Rating: rating, Detail: detail}
}

// emptyAnswer reports whether raw means "no answer" (empty or JSON null).
func emptyAnswer(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || string(trimmed) == "null"
}

// answerString decodes a string answer; no answer yields the empty string.
func answerString(raw json.RawMessage, control string) (string, error) {
	if emptyAnswer(raw) {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("%w: %s answer must be a string", ErrInvalidAnswer, control)
	}
	return value, nil
}

// answerIndex decodes a zero-based option index: a JSON integer, or a string
// whose content is an integer. No answer is an error.
func answerIndex(raw json.RawMessage, control string) (int, error) {
	if emptyAnswer(raw) {
		return 0, fmt.Errorf("%w: %s answer is required", ErrInvalidAnswer, control)
	}
	var index int
	if err := json.Unmarshal(raw, &index); err == nil {
		return index, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if value, err := strconv.Atoi(strings.TrimSpace(text)); err == nil {
			return value, nil
		}
	}
	return 0, fmt.Errorf("%w: %s answer must be an option index", ErrInvalidAnswer, control)
}

// answerBool decodes a JSON boolean.
func answerBool(raw json.RawMessage) (bool, error) {
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fmt.Errorf("%w: true_false answer must be a boolean", ErrInvalidAnswer)
	}
	return value, nil
}

// answerIntArray decodes an array of option indices; no answer is an empty
// array.
func answerIntArray(raw json.RawMessage, control string) ([]int, error) {
	indices := []int{}
	if !emptyAnswer(raw) {
		if err := json.Unmarshal(raw, &indices); err != nil {
			return nil, fmt.Errorf("%w: %s answer must be an array of option indices", ErrInvalidAnswer, control)
		}
	}
	return indices, nil
}

// answerStringArray decodes an array of strings; no answer is an empty array.
func answerStringArray(raw json.RawMessage, control string) ([]string, error) {
	values := []string{}
	if !emptyAnswer(raw) {
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, fmt.Errorf("%w: %s answer must be an array of strings", ErrInvalidAnswer, control)
		}
	}
	return values, nil
}

// normalizeAnswer folds case and collapses whitespace for comparison.
func normalizeAnswer(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

// matchesExpected reports whether the learner's answer equals one acceptable
// wording. The comparison ignores case and whitespace. When the whole expected
// wording is an inline formula \( ... \), the form without the delimiters also
// matches, because a typeset formula cannot be typed into an input box. An
// empty answer never matches. When unescape is true the Markdown escape \| is
// read as a literal pipe; list item wordings use that rule, cloze content does
// not because a pipe there is ordinary content.
func matchesExpected(given, expected string, unescape bool) bool {
	normalized := normalizeAnswer(given)
	if normalized == "" {
		return false
	}
	if unescape {
		expected = unescapePipe(expected)
	}
	if normalized == normalizeAnswer(expected) {
		return true
	}
	inner := strings.TrimSpace(expected)
	if len(inner) < 4 || !strings.HasPrefix(inner, `\(`) || !strings.HasSuffix(inner, `\)`) {
		return false
	}
	return normalized == normalizeAnswer(inner[2:len(inner)-2])
}

// unescapePipe turns the Markdown escape \| into a literal pipe for list
// comparison. Cloze content no longer splits on pipes, so the cloze control
// does not use this helper.
func unescapePipe(text string) string {
	return strings.ReplaceAll(text, `\|`, "|")
}

// mathSpanPattern matches the inline and block formula delimiters, so a pipe
// inside a formula stays content when a list item is split into alternatives.
// The answer package may depend only on notetype, so this pattern cannot call
// texmath.Ranges; it is the same expression, and a list test covers a formula
// that contains a pipe.
var mathSpanPattern = regexp.MustCompile(`(?s)\\\((?:\\.|[^\\])*?\\\)|\\\[(?:\\.|[^\\])*?\\\]`)

// splitAlternatives splits a list item into its acceptable wordings at a
// top-level | or \alt. A pipe inside a formula, inside nested {{ }} content, or
// written as an escape is not a separator. Empty wordings are dropped; when
// that leaves nothing, the item itself is the only wording.
func splitAlternatives(item string) []string {
	spans := mathSpanPattern.FindAllStringIndex(item, -1)
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(item); {
		if insideSpan(spans, i) {
			i++
			continue
		}
		if item[i] == '\\' && i+1 < len(item) {
			if depth == 0 && hasAltSeparator(item, i) {
				parts = append(parts, item[start:i])
				i += len(altSeparator)
				start = i
				continue
			}
			i += 2
			continue
		}
		switch {
		case strings.HasPrefix(item[i:], "{{"):
			depth++
			i += 2
		case strings.HasPrefix(item[i:], "}}") && depth > 0:
			depth--
			i += 2
		case item[i] == '|' && depth == 0:
			parts = append(parts, item[start:i])
			i++
			start = i
		default:
			i++
		}
	}
	parts = append(parts, item[start:])

	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			filtered = append(filtered, part)
		}
	}
	if len(filtered) == 0 {
		return []string{item}
	}
	return filtered
}

// altSeparator separates acceptable wordings in a list item.
const altSeparator = `\alt`

// hasAltSeparator reports whether item[i:] starts with \alt. A following ASCII
// letter makes it a different TeX command such as \alpha.
func hasAltSeparator(item string, i int) bool {
	if !strings.HasPrefix(item[i:], altSeparator) {
		return false
	}
	j := i + len(altSeparator)
	if j >= len(item) {
		return true
	}
	c := item[j]
	return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z')
}

// insideSpan reports whether position i lies inside one of the formula spans.
func insideSpan(spans [][]int, i int) bool {
	for _, span := range spans {
		if span[0] <= i && i < span[1] {
			return true
		}
	}
	return false
}
