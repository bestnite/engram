package cardtype

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
)

// This file carries the grading skeleton for answer-based card types: the score→rating level mapping, the grading input base type,
// and the numeric/integer read helpers for fields. The seven concrete card types are implemented in typed.go / numeric.go /
// choice.go / truefalse.go / cloze.go / list.go.

// The four FSRS rating levels, consistent with the integer convention of reviews.rating.
const (
	RatingAgain = 1
	RatingHard  = 2
	RatingGood  = 3
	RatingEasy  = 4
)

// GradeMapping is the "score → rating level" mapping, stored as JSON in preset.
//
// The score is the [0,1] correctness rate: 1 = all correct, 0 = all wrong, anything between is a partial score. The default mapping is
// all correct = Good, partially correct = Hard, all wrong = Again. Putting it in preset
// is for tuning per card type / deck, for example promoting all correct to Easy, or demoting partially correct to Again.
//
// Thresholds are optional: score >= FullThreshold counts as all correct, score <= NoneThreshold as all wrong,
// and anything between as a partial score. A zero or negative threshold falls back to the default (full threshold 1.0, none threshold 0.0).
type GradeMapping struct {
	Version       int     `json:"version"`
	Full          int     `json:"full"`
	Partial       int     `json:"partial"`
	None          int     `json:"none"`
	FullThreshold float64 `json:"full_threshold,omitempty"`
	NoneThreshold float64 `json:"none_threshold,omitempty"`
}

// GradeMappingVersion is the version of the mapping structure this package understands; unknown versions are rejected to avoid silent misreading.
const GradeMappingVersion = 1

// DefaultGradeMapping returns the default mapping: all correct Good, partial Hard, all wrong Again.
func DefaultGradeMapping() GradeMapping {
	return GradeMapping{
		Version:       GradeMappingVersion,
		Full:          RatingGood,
		Partial:       RatingHard,
		None:          RatingAgain,
		FullThreshold: 1.0,
		NoneThreshold: 0.0,
	}
}

// ParseGradeMapping parses the mapping JSON from preset. An empty string (column is NULL) falls back to the default mapping;
// this is the only entry point for the "missing → default" fallback.
func ParseGradeMapping(raw string) (GradeMapping, error) {
	m := DefaultGradeMapping()
	if raw == "" {
		return m, nil
	}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return GradeMapping{}, fmt.Errorf("grade mapping: invalid JSON: %w", err)
	}
	if err := m.validate(); err != nil {
		return GradeMapping{}, err
	}
	return m.withDefaults(), nil
}

// MarshalGradeMapping encodes the mapping into the JSON text stored in the database.
func MarshalGradeMapping(m GradeMapping) (string, error) {
	if err := m.validate(); err != nil {
		return "", err
	}
	b, err := json.Marshal(m.withDefaults())
	if err != nil {
		return "", fmt.Errorf("grade mapping: marshal: %w", err)
	}
	return string(b), nil
}

// withDefaults fills in ratings and thresholds that were not set (zero value).
func (m GradeMapping) withDefaults() GradeMapping {
	d := DefaultGradeMapping()
	if m.Version == 0 {
		m.Version = d.Version
	}
	if m.Full == 0 {
		m.Full = d.Full
	}
	if m.Partial == 0 {
		m.Partial = d.Partial
	}
	if m.None == 0 {
		m.None = d.None
	}
	if m.FullThreshold <= 0 {
		m.FullThreshold = d.FullThreshold
	}
	if m.NoneThreshold <= 0 {
		m.NoneThreshold = d.NoneThreshold
	}
	return m
}

// validate checks the rating values and threshold ordering; errors name the field so callers can locate it.
func (m GradeMapping) validate() error {
	if m.Version != 0 && m.Version != GradeMappingVersion {
		return fmt.Errorf("grade mapping: unsupported version %d", m.Version)
	}
	n := m.withDefaults()
	for name, rating := range map[string]int{"full": n.Full, "partial": n.Partial, "none": n.None} {
		if rating < RatingAgain || rating > RatingEasy {
			return fmt.Errorf("grade mapping: %s rating %d out of range 1-4", name, rating)
		}
	}
	if n.FullThreshold <= n.NoneThreshold {
		return fmt.Errorf("grade mapping: full_threshold (%g) must be greater than none_threshold (%g)", n.FullThreshold, n.NoneThreshold)
	}
	return nil
}

// RatingFor maps a score in [0,1] to a 1–4 rating level. An out-of-range score is clamped to [0,1].
func (m GradeMapping) RatingFor(score float64) int {
	n := m.withDefaults()
	if math.IsNaN(score) {
		return n.None
	}
	if score = math.Max(0, math.Min(1, score)); score >= n.FullThreshold {
		return n.Full
	}
	if score <= n.NoneThreshold {
		return n.None
	}
	return n.Partial
}

// GradeContext is the common part of an answer-based grading input: Fields are the note's fields (the correct answers, options and
// tolerance rules needed for grading are in there), and Mapping is the mapping of the preset the card type belongs to (nil = default).
//
// A grader only receives input, so the fields must come in with the input; that way the optional narrow Grader interface needs no expansion,
// and the core submission pipeline needs no change.
//
// Template is the template of the card being answered (for example cloze:2). When different cards of the same note test different content
// (a cloze card per index), the grader relies on it to find this card's tested points; other card types have one card per note and can ignore it.
type GradeContext struct {
	Fields   map[string]any
	Template string
	Mapping  *GradeMapping
}

// rating uses the mapping to turn the score into a rating and fill in the common fields of detail.
func (c GradeContext) rating(score float64, detail map[string]any) (int, map[string]any, bool) {
	m := DefaultGradeMapping()
	if c.Mapping != nil {
		m = *c.Mapping
	}
	r := m.RatingFor(score)
	if detail == nil {
		detail = map[string]any{}
	}
	detail["score"] = score
	detail["rating"] = r
	return r, detail, true
}

// numberField reads a required numeric field; supports both the float64 of JSON deserialization and Go-side constructed integer types.
func numberField(fields map[string]any, name string) (float64, error) {
	v, ok := fields[name]
	if !ok || v == nil {
		return 0, fmt.Errorf("missing required field %q", name)
	}
	f, ok := toFloat(v)
	if !ok {
		return 0, fmt.Errorf("field %q must be a number", name)
	}
	return f, nil
}

// optionalFloatField reads an optional numeric field; returns (nil, nil) when missing.
func optionalFloatField(fields map[string]any, name string) (*float64, error) {
	v, ok := fields[name]
	if !ok || v == nil {
		return nil, nil
	}
	f, ok := toFloat(v)
	if !ok {
		return nil, fmt.Errorf("field %q must be a number", name)
	}
	return &f, nil
}

// intField reads a required 0-based index field; it must be an integer value (float64 in JSON).
func intField(fields map[string]any, name string) (int, error) {
	v, ok := fields[name]
	if !ok || v == nil {
		return 0, fmt.Errorf("missing required field %q", name)
	}
	return toIndex(v, name)
}

// intSliceField reads a required array of 0-based indices (answers of choice_multi).
func intSliceField(fields map[string]any, name string) ([]int, error) {
	v, ok := fields[name]
	if !ok || v == nil {
		return nil, fmt.Errorf("missing required field %q", name)
	}
	raw, ok := v.([]any)
	if !ok {
		if is, ok := v.([]int); ok {
			if len(is) == 0 {
				return nil, fmt.Errorf("field %q must not be empty", name)
			}
			return is, nil
		}
		return nil, fmt.Errorf("field %q must be an array of integers", name)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("field %q must not be empty", name)
	}
	out := make([]int, len(raw))
	for i, item := range raw {
		idx, err := toIndex(item, fmt.Sprintf("%s[%d]", name, i))
		if err != nil {
			return nil, err
		}
		out[i] = idx
	}
	return out, nil
}

// requiredBoolField reads a required boolean field; unlike boolField, a missing value is an error (answer of true_false).
func requiredBoolField(fields map[string]any, name string) (bool, error) {
	v, ok := fields[name]
	if !ok || v == nil {
		return false, fmt.Errorf("missing required field %q", name)
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("field %q must be a boolean", name)
	}
	return b, nil
}

// toFloat normalizes the common numeric types from JSON/Go into float64.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// toIndex turns a value into a non-negative integer index, rejecting fractions and illegal types.
func toIndex(v any, name string) (int, error) {
	switch n := v.(type) {
	case int:
		if n < 0 {
			return 0, fmt.Errorf("field %q must be a non-negative integer", name)
		}
		return n, nil
	case int64:
		if n < 0 {
			return 0, fmt.Errorf("field %q must be a non-negative integer", name)
		}
		return int(n), nil
	case float64:
		if n < 0 || n != math.Trunc(n) {
			return 0, fmt.Errorf("field %q must be a non-negative integer", name)
		}
		return int(n), nil
	case json.Number:
		i, err := n.Int64()
		if err != nil || i < 0 {
			return 0, fmt.Errorf("field %q must be a non-negative integer", name)
		}
		return int(i), nil
	default:
		return 0, fmt.Errorf("field %q must be a non-negative integer", name)
	}
}

// formatNumber prints a number in its simplest form for rendering (50 is not written as 50.000000).
func formatNumber(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}
