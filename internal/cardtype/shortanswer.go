package cardtype

import (
	"encoding/json"
	"fmt"
	"strings"
)

// shortAnswerType is the subjective free-text card: prompt asks the question, and the learner first writes their own answer, then
// self-rates after comparing it with the optional reference answer ("subjective": self-rate first, LLM grading left for later).
//
// Writing before comparing is what distinguishes it from basic: writing the answer out tests recall better than running it through your head.
// The written answer is stored in the review record along with the self-rating (see RecordAnswer); when LLM grading is wired in, it grades this text.
//
// Fields: prompt (required), reference (optional reference answer), extra / source_url (optional).
// This card type deliberately does not implement Grader: it is not in the machine-grading set and the review flow takes the self-rating path for it;
// LLM grading will be wired in when this card type gains PromptContexter / Grader, and the core pipeline will not change then.
type shortAnswerType struct{}

// Label returns the language pack key.
func (shortAnswerType) Label() string { return "cardtype.short_answer" }

// Describe self-describes: prompt on the front, reference on the back; the answer control is free-form writing, and after writing the learner still self-rates
// (Grader is not implemented).
func (shortAnswerType) Describe() Description {
	return Description{
		Kind:          "short_answer",
		LabelKey:      "cardtype.short_answer",
		AnswerControl: AnswerEssay,
		FrontField:    "prompt",
		BackField:     "reference",
		PromptField:   "prompt",
		Fields: fieldsWithCommon(
			FieldSpec{Key: "prompt", Control: ControlTextarea, Required: true},
			FieldSpec{Key: "reference", Control: ControlTextarea},
		),
		Example: map[string]any{
			"prompt":    "Why does the Moon show phases?",
			"reference": "The Sun always lights half of the Moon; as the Moon orbits Earth we see a changing share of that lit half.",
		},
	}
}

// Validate checks prompt / reference. reference is an optional reference answer, and omitting it is valid.
func (shortAnswerType) Validate(fields map[string]any) error {
	if _, err := stringField(fields, "prompt"); err != nil {
		return fmt.Errorf("short_answer: %w", err)
	}
	if _, err := optionalStringField(fields, "reference"); err != nil {
		return fmt.Errorf("short_answer: %w", err)
	}
	if err := validateCommonOptional(fields); err != nil {
		return fmt.Errorf("short_answer: %w", err)
	}
	return nil
}

// Cards produces one forward card (frozen table: generated cards = 1).
func (shortAnswerType) Cards(note Note) []Card {
	return []Card{{Template: "forward", Ordinal: 0, Fields: note.Fields}}
}

// Render shows prompt on the front; the back gives only the reference answer, without repeating the prompt (the review page renders the back below the front).
// With no reference answer the back is empty and the review page only compares against the answer the learner wrote.
func (shortAnswerType) Render(card Card, side Side) (RenderResult, error) {
	if card.Template != "forward" {
		return RenderResult{}, errUnknownTemplate(card.Template)
	}
	prompt, err := stringField(card.Fields, "prompt")
	if err != nil {
		return RenderResult{}, fmt.Errorf("short_answer: %w", err)
	}
	if side == SideFront {
		return RenderResult{Body: prompt}, nil
	}
	ref, err := optionalStringField(card.Fields, "reference")
	if err != nil {
		return RenderResult{}, fmt.Errorf("short_answer: %w", err)
	}
	return RenderResult{Body: ref}, nil
}

// RecordAnswer decodes the answer the learner wrote before self-rating (a string) and returns the detail saved with the review record.
// Writing nothing (empty answer or whitespace only) returns nil: no empty answer is stored.
func (shortAnswerType) RecordAnswer(raw json.RawMessage) (map[string]any, error) {
	answer, err := answerString(raw, "short_answer")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(answer) == "" {
		return nil, nil
	}
	return map[string]any{"answer": answer}, nil
}

// optionalStringField reads an optional string field: missing or nil returns an empty string, and when present it must be a non-empty string.
// It is separate from stringField so that the "optional reference answer" is not mistaken for a required one (frozen table).
func optionalStringField(fields map[string]any, name string) (string, error) {
	v, ok := fields[name]
	if !ok || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("field %q must be a string", name)
	}
	if s == "" {
		return "", fmt.Errorf("field %q must not be empty", name)
	}
	return s, nil
}
