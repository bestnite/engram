package cardtype

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// numericType is the numeric-answer card: the user types a number and it is graded by absolute/relative tolerance.
// Fields: prompt / value (required), tolerance_absolute / tolerance_relative / unit (optional),
// extra / source_url (optional). This grading directly serves purely memorized mappings such as "fraction ↔ percentage".
type numericType struct{}

// Label returns the language pack key.
func (numericType) Label() string { return "cardtype.numeric" }

// Describe self-describes: prompt on the front, value on the back; the answer control is numeric and grading comes from the Grader.
func (numericType) Describe() Description {
	return Description{
		Kind:          "numeric",
		LabelKey:      "cardtype.numeric",
		AnswerControl: AnswerNumber,
		FrontField:    "prompt",
		BackField:     "value",
		PromptField:   "prompt",
		Fields: fieldsWithCommon(
			FieldSpec{Key: "prompt", Control: ControlTextarea, Required: true},
			FieldSpec{Key: "value", Control: ControlNumber, Required: true},
			FieldSpec{Key: "unit", Control: ControlText},
			FieldSpec{Key: "tolerance_absolute", Control: ControlNumber},
			FieldSpec{Key: "tolerance_relative", Control: ControlNumber},
		),
		Example: map[string]any{
			"prompt":             "What is standard gravity \\(g_0\\)?",
			"value":              9.80665,
			"unit":               "m/s²",
			"tolerance_relative": 0.01,
		},
	}
}

// Validate checks prompt / value and the tolerance rules; the relative tolerance is a ratio in 0–1.
func (numericType) Validate(fields map[string]any) error {
	if _, err := stringField(fields, "prompt"); err != nil {
		return fmt.Errorf("numeric: %w", err)
	}
	if _, err := numberField(fields, "value"); err != nil {
		return fmt.Errorf("numeric: %w", err)
	}
	if tol, err := optionalFloatField(fields, "tolerance_absolute"); err != nil {
		return fmt.Errorf("numeric: %w", err)
	} else if tol != nil && *tol < 0 {
		return fmt.Errorf("numeric: field %q must not be negative", "tolerance_absolute")
	}
	if tol, err := optionalFloatField(fields, "tolerance_relative"); err != nil {
		return fmt.Errorf("numeric: %w", err)
	} else if tol != nil && (*tol < 0 || *tol > 1) {
		return fmt.Errorf("numeric: field %q must be between 0 and 1", "tolerance_relative")
	}
	if v, ok := fields["unit"]; ok && v != nil {
		if s, ok := v.(string); !ok || s == "" {
			return fmt.Errorf(`numeric: field "unit" must be a non-empty string`)
		}
	}
	if err := validateCommonOptional(fields); err != nil {
		return fmt.Errorf("numeric: %w", err)
	}
	return nil
}

// Cards produces one forward card.
func (numericType) Cards(note Note) []Card {
	return []Card{{Template: "forward", Ordinal: 0, Fields: note.Fields}}
}

// Render gives the prompt on the front; the back gives the number, appending the unit when unit is set.
func (numericType) Render(card Card, side Side) (RenderResult, error) {
	if card.Template != "forward" {
		return RenderResult{}, errUnknownTemplate(card.Template)
	}
	prompt, err := stringField(card.Fields, "prompt")
	if err != nil {
		return RenderResult{}, fmt.Errorf("numeric: %w", err)
	}
	if side == SideFront {
		return RenderResult{Body: prompt}, nil
	}
	value, err := numberField(card.Fields, "value")
	if err != nil {
		return RenderResult{}, fmt.Errorf("numeric: %w", err)
	}
	body := formatNumber(value)
	if unit, ok := card.Fields["unit"].(string); ok && unit != "" {
		body += " " + unit
	}
	return RenderResult{Body: body}, nil
}

// NumericInput is the input of numericType.Grade.
type NumericInput struct {
	GradeContext
	Answer string
}

// ParseAnswer decodes the answer: usually a string (possibly with a unit), and it also accepts a bare JSON number, keeping the source text;
// no answer is graded as an empty string.
func (numericType) ParseAnswer(gc GradeContext, raw json.RawMessage) (any, error) {
	if emptyAnswer(raw) {
		return NumericInput{GradeContext: gc}, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return NumericInput{GradeContext: gc, Answer: s}, nil
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return nil, fmt.Errorf("numeric answer must be a string or number")
	}
	return NumericInput{GradeContext: gc, Answer: n.String()}, nil
}

// GivenText returns the user input verbatim.
func (numericType) GivenText(_ map[string]any, detail map[string]any) string {
	given, _ := detail["given"].(string)
	return given
}

// Grade grades: correct when |answer - value| does not exceed the larger of the absolute tolerance and the relative tolerance (the relative one being a ratio of |value|).
// The answer may carry the configured unit suffix, which is stripped before comparison.
func (numericType) Grade(input any) (int, map[string]any, bool) {
	in, ok := input.(NumericInput)
	if !ok {
		return 0, nil, false
	}
	value, err := numberField(in.Fields, "value")
	if err != nil {
		return 0, nil, false
	}
	absTol := 0.0
	if t, err := optionalFloatField(in.Fields, "tolerance_absolute"); err != nil {
		return 0, nil, false
	} else if t != nil {
		absTol = *t
	}
	relTol := 0.0
	if t, err := optionalFloatField(in.Fields, "tolerance_relative"); err != nil {
		return 0, nil, false
	} else if t != nil {
		relTol = *t
	}
	unit, _ := in.Fields["unit"].(string)

	detail := map[string]any{"value": value, "given": in.Answer}
	if absTol > 0 {
		detail["tolerance_absolute"] = absTol
	}
	if relTol > 0 {
		detail["tolerance_relative"] = relTol
	}
	if unit != "" {
		detail["unit"] = unit
	}

	answer, parseOK := parseNumericAnswer(in.Answer, unit)
	if !parseOK {
		detail["parsed"] = false
		return in.rating(0, detail)
	}
	detail["parsed_answer"] = answer

	allowed := absTol
	if rel := relTol * math.Abs(value); rel > allowed {
		allowed = rel
	}
	if math.Abs(answer-value) <= allowed {
		return in.rating(1, detail)
	}
	return in.rating(0, detail)
}

// PromptContexter supplies the question and reference answer for future LLM scoring.
func (numericType) PromptContext(note Note) PromptContext {
	prompt, _ := stringField(note.Fields, "prompt")
	value, err := numberField(note.Fields, "value")
	reference := ""
	if err == nil {
		reference = formatNumber(value)
		if unit, ok := note.Fields["unit"].(string); ok && unit != "" {
			reference += " " + unit
		}
	}
	return PromptContext{Question: prompt, Reference: reference}
}

// parseNumericAnswer parses the user input into a number: trims surrounding whitespace and strips the optional unit suffix.
// Returning false means the input is not a comparable number (treated as wrong, counted as Again).
func parseNumericAnswer(raw, unit string) (float64, bool) {
	s := strings.TrimSpace(raw)
	if unit != "" && strings.HasSuffix(strings.ToLower(s), strings.ToLower(unit)) {
		s = strings.TrimSpace(s[:len(s)-len(unit)])
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}
