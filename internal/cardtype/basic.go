package cardtype

import "fmt"

// basicType 是基础问答卡：front 提问、back 回答，产出 1 张卡。
// 字段：front / back（必填），extra / source_url（可选）。
type basicType struct{}

// Label 返回语言包键名。
func (basicType) Label() string { return "cardtype.basic" }

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
