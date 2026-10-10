package cardtype

import "fmt"

// basicType 是基础问答卡：front 提问、back 回答，产出 1 张卡。
// 字段：front / back（必填），extra / source_url（可选）。
type basicType struct{}

// Label 返回语言包键名。
func (basicType) Label() string { return "cardtype.basic" }

// Describe 自描述：正反面是 front/back，自评题型（不实现 Grader）。
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

// Validate 校验 front / back。
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

// Cards 产出唯一的正向卡。
func (basicType) Cards(note Note) []Card {
	return []Card{{Template: "forward", Ordinal: 0, Fields: note.Fields}}
}

// Render 正面显示 front，背面显示 back。
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

// basicBothType 是双向问答卡：正反各一张。
// 字段：front / back（必填），extra / source_url（可选）。
type basicBothType struct{}

// Label 返回语言包键名。
func (basicBothType) Label() string { return "cardtype.basic_both" }

// Describe 自描述：正反面是 front/back；反向卡是模板层的事，不改变字段映射。
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

// Validate 校验 front / back。
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

// Cards 产出正向（ordinal 0）与反向（ordinal 1）两张卡。
func (basicBothType) Cards(note Note) []Card {
	return []Card{
		{Template: "forward", Ordinal: 0, Fields: note.Fields},
		{Template: "reverse", Ordinal: 1, Fields: note.Fields},
	}
}

// Render 正向 front→back；反向把 back 当问题、front 当答案。
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
