package answer

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"git.nite07.com/nite/engram/internal/notetype"
)

// Kind is how a bound field value is parsed.
type Kind int

const (
	// KindText is PlainText of the field, trimmed.
	KindText Kind = iota
	// KindLines is the HTML split at line breaks, each line converted with
	// PlainText, trimmed, with empty lines dropped.
	KindLines
	// KindBool is the lowercased PlainText equal to "true" or "false".
	KindBool
	// KindNumber is the PlainText parsed as a decimal.
	KindNumber
)

// Role is one binding a control reads: its name, how the field value is
// parsed, and whether the answer spec must bind it.
type Role struct {
	Name     string
	Kind     Kind
	Required bool
}

// Binding is one resolved role: the field value as stored, and its parsed
// form. Value is a string for KindText, []string for KindLines, bool for
// KindBool and float64 for KindNumber.
type Binding struct {
	HTML  string
	Value any
}

// Bindings maps role names to their resolved values.
type Bindings map[string]Binding

// RequiredText returns the parsed text of a required role.
func (b Bindings) RequiredText(role string) (string, error) {
	value, err := b.required(role)
	if err != nil {
		return "", err
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("answer: role %q is not text", role)
	}
	return text, nil
}

// RequiredLines returns the parsed lines of a required role.
func (b Bindings) RequiredLines(role string) ([]string, error) {
	value, err := b.required(role)
	if err != nil {
		return nil, err
	}
	lines, ok := value.([]string)
	if !ok {
		return nil, fmt.Errorf("answer: role %q is not lines", role)
	}
	return lines, nil
}

// RequiredBool returns the parsed boolean of a required role.
func (b Bindings) RequiredBool(role string) (bool, error) {
	value, err := b.required(role)
	if err != nil {
		return false, err
	}
	parsed, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("answer: role %q is not a boolean", role)
	}
	return parsed, nil
}

// RequiredNumber returns the parsed number of a required role.
func (b Bindings) RequiredNumber(role string) (float64, error) {
	value, err := b.required(role)
	if err != nil {
		return 0, err
	}
	number, ok := value.(float64)
	if !ok {
		return 0, fmt.Errorf("answer: role %q is not a number", role)
	}
	return number, nil
}

// OptionalText returns the parsed text of an optional role, or the empty
// string when the role is not bound.
func (b Bindings) OptionalText(role string) string {
	if binding, ok := b[role]; ok {
		if text, ok := binding.Value.(string); ok {
			return text
		}
	}
	return ""
}

// OptionalLines returns the parsed lines of an optional role, or nil when the
// role is not bound.
func (b Bindings) OptionalLines(role string) []string {
	if binding, ok := b[role]; ok {
		if lines, ok := binding.Value.([]string); ok {
			return lines
		}
	}
	return nil
}

// OptionalBool returns the parsed boolean of an optional role, or def when the
// role is not bound.
func (b Bindings) OptionalBool(role string, def bool) bool {
	if binding, ok := b[role]; ok {
		if value, ok := binding.Value.(bool); ok {
			return value
		}
	}
	return def
}

// OptionalNumber returns the parsed number of an optional role, or nil when the
// role is not bound.
func (b Bindings) OptionalNumber(role string) *float64 {
	if binding, ok := b[role]; ok {
		if value, ok := binding.Value.(float64); ok {
			return &value
		}
	}
	return nil
}

// Bound reports whether a role was resolved.
func (b Bindings) Bound(role string) bool {
	_, ok := b[role]
	return ok
}

func (b Bindings) required(role string) (any, error) {
	binding, ok := b[role]
	if !ok {
		return nil, fmt.Errorf("answer: missing required role %q", role)
	}
	return binding.Value, nil
}

// RenderContext is the input of one side's render. Bindings are already
// resolved and parsed. Marker is the exact type placeholder the renderer
// emitted (for example "[[type:answer]]"); the caller substitutes the returned
// markup for it. ElementID is the stable id of the input element the frame
// reads; Font and FontSize mirror the field's display settings. Hints holds an
// optional placeholder for each input a group control renders, in input order;
// a missing or empty entry adds no placeholder. ClozeOrdinal is the 1-based
// cloze number of the card and is zero for every other card. OptionOrder is the
// display order of the choice options: slot b shows stored option
// OptionOrder[b]. Answer and Result are set when the back is rendered after a
// submission; Result is nil on the front.
type RenderContext struct {
	Marker       string
	Side         notetype.Side
	Bindings     Bindings
	ElementID    string
	Font         string
	FontSize     int
	Hints        []string
	ClozeOrdinal int
	OptionOrder  []int
	Answer       json.RawMessage
	Result       *GradeResult
}

// GradeContext is the input of one grading pass. Answer is the JSON the frame
// posted; Mapping is the preset's grade mapping, with nil meaning the default.
// ClozeOrdinal and OptionOrder have the same meaning as in RenderContext.
type GradeContext struct {
	Bindings     Bindings
	Answer       json.RawMessage
	Mapping      *GradeMapping
	ClozeOrdinal int
	OptionOrder  []int
}

// lineBreakPattern matches the markup that starts a new line for KindLines:
// a <br>, an opening or closing <div> or <p>, and a literal newline. Other
// tags are inline and stay on the current line.
var lineBreakPattern = regexp.MustCompile(`(?is)<br\s*/?>|</?(?:div|p)(?:\s[^>]*)?>|\r?\n`)

// ParseText returns the plain-text form of an HTML field value, trimmed.
func ParseText(html string) string {
	return strings.TrimSpace(notetype.PlainText(html))
}

// ParseLines splits an HTML field value at line breaks, converts each line with
// PlainText, trims it, and drops empty lines. A <br>, a <div> or <p> boundary
// and a literal newline all start a new line.
func ParseLines(html string) []string {
	segments := lineBreakPattern.Split(html, -1)
	lines := make([]string, 0, len(segments))
	for _, segment := range segments {
		line := ParseText(segment)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

// ParseBool parses a boolean field value: the lowercased PlainText must be
// "true" or "false". Any other text is an error.
func ParseBool(html string) (bool, error) {
	text := strings.ToLower(ParseText(html))
	switch text {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%q is not a boolean", ParseText(html))
	}
}

// ParseNumber parses a numeric field value as a decimal. NaN and the infinities
// are not decimals and are rejected.
func ParseNumber(html string) (float64, error) {
	text := ParseText(html)
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("%q is not a number", text)
	}
	return value, nil
}

// parseValue applies the parse a role declares.
func parseValue(kind Kind, html string) (any, error) {
	switch kind {
	case KindText:
		return ParseText(html), nil
	case KindLines:
		return ParseLines(html), nil
	case KindBool:
		return ParseBool(html)
	case KindNumber:
		return ParseNumber(html)
	default:
		return nil, fmt.Errorf("unknown role kind %d", kind)
	}
}

// optionIndex converts a parsed number to a zero-based option index. A
// fraction or a negative value is not an index.
func optionIndex(value float64, role string) (int, error) {
	if value < 0 || value != math.Trunc(value) {
		return 0, fmt.Errorf("role %q must be a non-negative whole number", role)
	}
	return int(value), nil
}

// optionIndices converts parsed lines to zero-based option indices.
func optionIndices(lines []string, role string) ([]int, error) {
	indices := make([]int, 0, len(lines))
	for i, line := range lines {
		value, err := ParseNumber(line)
		if err != nil {
			return nil, fmt.Errorf("role %q[%d]: %w", role, i, err)
		}
		index, err := optionIndex(value, fmt.Sprintf("%s[%d]", role, i))
		if err != nil {
			return nil, err
		}
		indices = append(indices, index)
	}
	return indices, nil
}
