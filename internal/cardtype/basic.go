package cardtype

import "fmt"

// basicType is the basic Q&A card: front asks, back answers, producing 1 card.
// Fields: front / back (required), extra / source_url (optional).
type basicType struct{}

// Label returns the language pack key.
func (basicType) Label() string { return "cardtype.basic" }

// Describe self-describes: the front and back are front/back, a self-graded card type (does not implement Grader).
func (basicType) Describe() Description {
	return Description{
		Kind:          "basic",
		LabelKey:      "cardtype.basic",
		AnswerControl: AnswerNone,
		FrontField:    "front",
		BackField:     "back",
		Fields: fieldsWithCommon(
			FieldSpec{Key: "front", Control: ControlTextarea, Required: true},
			FieldSpec{Key: "back", Control: ControlTextarea, Required: true},
		),
		Example: map[string]any{
			"front":      "What is the capital of France?",
			"back":       "Paris",
			"extra":      map[string]any{"note": "capital since the late 10th century"},
			"source_url": "https://example.com/geography/france",
		},
	}
}

// Validate checks front / back.
func (basicType) Validate(fields map[string]any) error {
	if _, err := stringField(fields, "front"); err != nil {
		return fmt.Errorf("basic: %w", err)
	}
	if _, err := stringField(fields, "back"); err != nil {
		return fmt.Errorf("basic: %w", err)
	}
	if err := validateCommonOptional(fields); err != nil {
		return fmt.Errorf("basic: %w", err)
	}
	return nil
}

// Cards produces the single forward card.
func (basicType) Cards(note Note) []Card {
	return []Card{{Template: "forward", Ordinal: 0, Fields: note.Fields}}
}

// Render shows front on the front side and back on the back side.
func (basicType) Render(card Card, side Side) (RenderResult, error) {
	if card.Template != "forward" {
		return RenderResult{}, errUnknownTemplate(card.Template)
	}
	front, err := stringField(card.Fields, "front")
	if err != nil {
		return RenderResult{}, fmt.Errorf("basic: %w", err)
	}
	back, err := stringField(card.Fields, "back")
	if err != nil {
		return RenderResult{}, fmt.Errorf("basic: %w", err)
	}
	if side == SideBack {
		return RenderResult{Body: back}, nil
	}
	return RenderResult{Body: front}, nil
}

// basicBothType is the bidirectional Q&A card: one card in each direction.
// Fields: front / back (required), extra / source_url (optional).
type basicBothType struct{}

// Label returns the language pack key.
func (basicBothType) Label() string { return "cardtype.basic_both" }

// Describe self-describes: the front and back are front/back; the reverse card is the template layer's business and does not change the field mapping.
func (basicBothType) Describe() Description {
	return Description{
		Kind:          "basic_both",
		LabelKey:      "cardtype.basic_both",
		AnswerControl: AnswerNone,
		FrontField:    "front",
		BackField:     "back",
		Fields: fieldsWithCommon(
			FieldSpec{Key: "front", Control: ControlTextarea, Required: true},
			FieldSpec{Key: "back", Control: ControlTextarea, Required: true},
		),
		Example: map[string]any{
			"front": "der Hund",
			"back":  "the dog",
		},
	}
}

// Validate checks front / back.
func (basicBothType) Validate(fields map[string]any) error {
	if _, err := stringField(fields, "front"); err != nil {
		return fmt.Errorf("basic_both: %w", err)
	}
	if _, err := stringField(fields, "back"); err != nil {
		return fmt.Errorf("basic_both: %w", err)
	}
	if err := validateCommonOptional(fields); err != nil {
		return fmt.Errorf("basic_both: %w", err)
	}
	return nil
}

// Cards produces two cards: forward (ordinal 0) and reverse (ordinal 1).
func (basicBothType) Cards(note Note) []Card {
	return []Card{
		{Template: "forward", Ordinal: 0, Fields: note.Fields},
		{Template: "reverse", Ordinal: 1, Fields: note.Fields},
	}
}

// Render goes front→back forward; reverse treats back as the question and front as the answer.
func (basicBothType) Render(card Card, side Side) (RenderResult, error) {
	front, err := stringField(card.Fields, "front")
	if err != nil {
		return RenderResult{}, fmt.Errorf("basic_both: %w", err)
	}
	back, err := stringField(card.Fields, "back")
	if err != nil {
		return RenderResult{}, fmt.Errorf("basic_both: %w", err)
	}
	var question, answer string
	switch card.Template {
	case "forward":
		question, answer = front, back
	case "reverse":
		question, answer = back, front
	default:
		return RenderResult{}, errUnknownTemplate(card.Template)
	}
	if side == SideBack {
		return RenderResult{Body: answer}, nil
	}
	return RenderResult{Body: question}, nil
}
