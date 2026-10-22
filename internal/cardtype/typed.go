package cardtype

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// typedType is the typed-answer card: the user types text and the machine grades it.
// Fields: prompt / answer (required), accept[] / ignore_case / ignore_whitespace / regex (optional),
// extra / source_url (optional).
type typedType struct{}

// Label returns the language pack key.
func (typedType) Label() string { return "cardtype.typed" }

// Describe self-describes: prompt on the front, answer on the back; the answer control is text and grading comes from the Grader.
func (typedType) Describe() Description {
	return Description{
		Kind:          "typed",
		LabelKey:      "cardtype.typed",
		AnswerControl: AnswerText,
		FrontField:    "prompt",
		BackField:     "answer",
		PromptField:   "prompt",
		Fields: fieldsWithCommon(
			FieldSpec{Key: "prompt", Control: ControlTextarea, Required: true},
			FieldSpec{Key: "answer", Control: ControlText, Required: true},
			FieldSpec{Key: "accept", Control: ControlLines},
			// The server-side default ignores case and whitespace differences (the third argument of typedType's boolField is true).
			FieldSpec{Key: "ignore_case", Control: ControlBool, Default: true},
			FieldSpec{Key: "ignore_whitespace", Control: ControlBool, Default: true},
		),
		Example: map[string]any{
			"prompt":            "Which element has the chemical symbol Au?",
			"answer":            "gold",
			"accept":            []any{"aurum"},
			"ignore_case":       true,
			"ignore_whitespace": true,
		},
	}
}

// typedSettings are the tolerance rules used by one grading pass.
type typedSettings struct {
	answer           string
	accept           []string
	ignoreCase       bool
	ignoreWhitespace bool
	regex            *regexp.Regexp
}

// Validate checks prompt / answer and the tolerance rules; an invalid regex names that field.
func (typedType) Validate(fields map[string]any) error {
	if _, err := stringField(fields, "prompt"); err != nil {
		return fmt.Errorf("typed: %w", err)
	}
	if _, err := stringField(fields, "answer"); err != nil {
		return fmt.Errorf("typed: %w", err)
	}
	if _, err := optionalStringSliceField(fields, "accept"); err != nil {
		return fmt.Errorf("typed: %w", err)
	}
	if _, err := boolField(fields, "ignore_case", true); err != nil {
		return fmt.Errorf("typed: %w", err)
	}
	if _, err := boolField(fields, "ignore_whitespace", true); err != nil {
		return fmt.Errorf("typed: %w", err)
	}
	if _, err := typedRegex(fields); err != nil {
		return fmt.Errorf("typed: %w", err)
	}
	if err := validateCommonOptional(fields); err != nil {
		return fmt.Errorf("typed: %w", err)
	}
	return nil
}

// Cards produces one forward card.
func (typedType) Cards(note Note) []Card {
	return []Card{{Template: "forward", Ordinal: 0, Fields: note.Fields}}
}

// Render gives the prompt on the front; the back writes the canonical answer and the other acceptable answers on the same line, separated by " / "
// (consistent with the multiple wordings of cloze and list cards), rather than one answer per paragraph.
func (typedType) Render(card Card, side Side) (RenderResult, error) {
	if card.Template != "forward" {
		return RenderResult{}, errUnknownTemplate(card.Template)
	}
	prompt, err := stringField(card.Fields, "prompt")
	if err != nil {
		return RenderResult{}, fmt.Errorf("typed: %w", err)
	}
	if side == SideFront {
		return RenderResult{Body: prompt}, nil
	}
	answer, err := stringField(card.Fields, "answer")
	if err != nil {
		return RenderResult{}, fmt.Errorf("typed: %w", err)
	}
	accept, err := optionalStringSliceField(card.Fields, "accept")
	if err != nil {
		return RenderResult{}, fmt.Errorf("typed: %w", err)
	}
	return RenderResult{Body: strings.Join(append([]string{answer}, accept...), " / ")}, nil
}

// TypedInput is the input of typedType.Grade: the raw text the user typed plus the owning note's fields and mapping.
type TypedInput struct {
	GradeContext
	Answer string
}

// ParseAnswer decodes the answer: a string; no answer is graded as an empty string (counted all wrong) rather than rejected.
func (typedType) ParseAnswer(gc GradeContext, raw json.RawMessage) (any, error) {
	s, err := answerString(raw, "typed")
	if err != nil {
		return nil, err
	}
	return TypedInput{GradeContext: gc, Answer: s}, nil
}

// GivenText returns the user input verbatim.
func (typedType) GivenText(_ map[string]any, detail map[string]any) string {
	given, _ := detail["given"].(string)
	return given
}

// Grade grades: regex takes precedence over literal comparison; literal comparison normalizes by ignore_case / ignore_whitespace and then
// compares against answer and accept[] in turn. A hit scores 1, otherwise 0, and the rating level follows the mapping.
func (typedType) Grade(input any) (int, map[string]any, bool) {
	in, ok := input.(TypedInput)
	if !ok {
		return 0, nil, false
	}
	settings, err := parseTypedSettings(in.Fields)
	if err != nil {
		return 0, nil, false
	}
	given := normalizeTyped(in.Answer, settings.ignoreCase, settings.ignoreWhitespace)
	detail := map[string]any{"answer": settings.answer, "given": in.Answer}

	if settings.regex != nil {
		matched := settings.regex.MatchString(given)
		detail["matched"] = "regex"
		if !matched {
			return in.rating(0, detail)
		}
		return in.rating(1, detail)
	}

	for i, candidate := range settings.accept {
		if normalizeTyped(candidate, settings.ignoreCase, settings.ignoreWhitespace) == given {
			detail["matched"] = fmt.Sprintf("accept[%d]", i)
			return in.rating(1, detail)
		}
	}
	if normalizeTyped(settings.answer, settings.ignoreCase, settings.ignoreWhitespace) == given {
		detail["matched"] = "answer"
		return in.rating(1, detail)
	}
	return in.rating(0, detail)
}

// PromptContexter supplies the question and reference answer for future LLM scoring.
func (typedType) PromptContext(note Note) PromptContext {
	prompt, _ := stringField(note.Fields, "prompt")
	answer, _ := stringField(note.Fields, "answer")
	return PromptContext{Question: prompt, Reference: answer}
}

// parseTypedSettings reads and validates the grading rules.
func parseTypedSettings(fields map[string]any) (typedSettings, error) {
	answer, err := stringField(fields, "answer")
	if err != nil {
		return typedSettings{}, err
	}
	accept, err := optionalStringSliceField(fields, "accept")
	if err != nil {
		return typedSettings{}, err
	}
	ignoreCase, err := boolField(fields, "ignore_case", true)
	if err != nil {
		return typedSettings{}, err
	}
	ignoreWhitespace, err := boolField(fields, "ignore_whitespace", true)
	if err != nil {
		return typedSettings{}, err
	}
	re, err := typedRegex(fields)
	if err != nil {
		return typedSettings{}, err
	}
	return typedSettings{answer: answer, accept: accept, ignoreCase: ignoreCase, ignoreWhitespace: ignoreWhitespace, regex: re}, nil
}

// typedRegex parses the optional regex field; when ignore_case applies it adds a (?i) prefix and requires a whole-string match.
func typedRegex(fields map[string]any) (*regexp.Regexp, error) {
	v, ok := fields["regex"]
	if !ok || v == nil {
		return nil, nil
	}
	pattern, ok := v.(string)
	if !ok || pattern == "" {
		return nil, fmt.Errorf(`field "regex" must be a non-empty string`)
	}
	ignoreCase, err := boolField(fields, "ignore_case", true)
	if err != nil {
		return nil, err
	}
	if ignoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(`\A(?:` + pattern + `)\z`)
	if err != nil {
		return nil, fmt.Errorf(`field "regex" is not a valid regular expression: %w`, err)
	}
	return re, nil
}

// normalizeTyped normalizes the text to compare according to the rules: with ignore_whitespace it collapses whitespace and trims,
// otherwise it is left as-is (not trimmed); case folding is optional.
func normalizeTyped(s string, ignoreCase, ignoreWhitespace bool) string {
	if ignoreWhitespace {
		s = strings.Join(strings.Fields(s), " ")
	}
	if ignoreCase {
		s = strings.ToLower(s)
	}
	return s
}

// optionalStringSliceField reads an optional string array; missing returns nil, and an empty array is also accepted (accept may be empty).
func optionalStringSliceField(fields map[string]any, name string) ([]string, error) {
	v, ok := fields[name]
	if !ok || v == nil {
		return nil, nil
	}
	switch ss := v.(type) {
	case []string:
		return ss, nil
	case []any:
		out := make([]string, len(ss))
		for i, item := range ss {
			s, ok := item.(string)
			if !ok || s == "" {
				return nil, fmt.Errorf("field %q[%d] must be a non-empty string", name, i)
			}
			out[i] = s
		}
		return out, nil
	default:
		return nil, fmt.Errorf("field %q must be an array of strings", name)
	}
}
