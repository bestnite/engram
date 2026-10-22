package cardtype

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// trueFalseType is the true/false card: the user decides whether statement is true.
// Fields: statement / answer (bool, required), extra / source_url (optional).
type trueFalseType struct{}

// Label returns the language pack key.
func (trueFalseType) Label() string { return "cardtype.true_false" }

// Describe self-describes: statement on the front, answer on the back; the answer control is a boolean true/false question.
func (trueFalseType) Describe() Description {
	return Description{
		Kind:          "true_false",
		LabelKey:      "cardtype.true_false",
		AnswerControl: AnswerBool,
		FrontField:    "statement",
		BackField:     "answer",
		PromptField:   "statement",
		Fields: fieldsWithCommon(
			FieldSpec{Key: "statement", Control: ControlTextarea, Required: true},
			FieldSpec{Key: "answer", Control: ControlBool, Required: true},
		),
		Example: map[string]any{
			"statement": "Water boils at 100 °C at sea level.",
			"answer":    true,
		},
	}
}

// Validate checks statement and the boolean answer.
func (trueFalseType) Validate(fields map[string]any) error {
	if _, err := stringField(fields, "statement"); err != nil {
		return fmt.Errorf("true_false: %w", err)
	}
	if _, err := requiredBoolField(fields, "answer"); err != nil {
		return fmt.Errorf("true_false: %w", err)
	}
	if err := validateCommonOptional(fields); err != nil {
		return fmt.Errorf("true_false: %w", err)
	}
	return nil
}

// Cards produces one forward card.
func (trueFalseType) Cards(note Note) []Card {
	return []Card{{Template: "forward", Ordinal: 0, Fields: note.Fields}}
}

// Render gives the statement on the front and true/false on the back. True/false is the content itself; localization is the view layer's job.
func (trueFalseType) Render(card Card, side Side) (RenderResult, error) {
	if card.Template != "forward" {
		return RenderResult{}, errUnknownTemplate(card.Template)
	}
	statement, err := stringField(card.Fields, "statement")
	if err != nil {
		return RenderResult{}, fmt.Errorf("true_false: %w", err)
	}
	if side == SideFront {
		return RenderResult{Body: statement}, nil
	}
	answer, err := requiredBoolField(card.Fields, "answer")
	if err != nil {
		return RenderResult{}, fmt.Errorf("true_false: %w", err)
	}
	return RenderResult{Body: strconv.FormatBool(answer)}, nil
}

// TrueFalseInput is the input of trueFalseType.Grade; a nil Answer means unanswered and ungradable.
type TrueFalseInput struct {
	GradeContext
	Answer *bool
}

// ParseAnswer decodes the answer: a JSON boolean; with no answer it cannot be graded, so an error is returned.
func (trueFalseType) ParseAnswer(gc GradeContext, raw json.RawMessage) (any, error) {
	if emptyAnswer(raw) {
		return nil, fmt.Errorf("true_false answer is required")
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("true_false answer must be a boolean")
	}
	return TrueFalseInput{GradeContext: gc, Answer: &b}, nil
}

// GivenText returns "true" / "false"; localization is the front end's job.
func (trueFalseType) GivenText(_ map[string]any, detail map[string]any) string {
	if b, ok := detail["selected"].(bool); ok {
		return strconv.FormatBool(b)
	}
	return ""
}

// Grade grades: a correct judgement scores 1, a wrong one 0.
func (trueFalseType) Grade(input any) (int, map[string]any, bool) {
	in, ok := input.(TrueFalseInput)
	if !ok || in.Answer == nil {
		return 0, nil, false
	}
	answer, err := requiredBoolField(in.Fields, "answer")
	if err != nil {
		return 0, nil, false
	}
	detail := map[string]any{"answer": answer, "selected": *in.Answer}
	if answer == *in.Answer {
		return in.rating(1, detail)
	}
	return in.rating(0, detail)
}

// PromptContexter supplies the question and reference answer for future LLM scoring.
func (trueFalseType) PromptContext(note Note) PromptContext {
	statement, _ := stringField(note.Fields, "statement")
	answer, _ := requiredBoolField(note.Fields, "answer")
	return PromptContext{Question: statement, Reference: strconv.FormatBool(answer)}
}
