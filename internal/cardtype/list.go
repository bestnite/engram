package cardtype

import "fmt"

// listType 是列表卡：给出 prompt，逐项揭示 items。
// 字段：prompt（必填）、items[]（必填、非空）、ordered（可选 bool，缺省 false），
// extra / source_url（可选）。
type listType struct{}

// Label 返回语言包键名。
func (listType) Label() string { return "cardtype.list" }

// Describe 自描述：正面 prompt、背面逐项揭示 items；自评题型。
func (listType) Describe() Description {
	return Description{
		Kind:          "list",
		LabelKey:      "cardtype.list",
		AnswerControl: AnswerNone,
		FrontField:    "prompt",
		BackField:     "items",
		Fields: fieldsWithCommon(
			FieldSpec{Key: "prompt", Control: ControlTextarea, Required: true},
			FieldSpec{Key: "items", Control: ControlLines, Required: true},
			// ordered 的服务端默认是 false（cardtype.listType 的 boolField 第三参）。
			FieldSpec{Key: "ordered", Control: ControlBool, Default: false},
		),
	}
}

// Validate 校验 prompt / items / ordered。
func (listType) Validate(fields map[string]any) error {
	if _, err := stringField(fields, "prompt"); err != nil {
		return fmt.Errorf("list: %w", err)
	}
	if _, err := stringSliceField(fields, "items"); err != nil {
		return fmt.Errorf("list: %w", err)
	}
	// ordered 缺省 false（冻结表）。
	if _, err := boolField(fields, "ordered", false); err != nil {
		return fmt.Errorf("list: %w", err)
	}
	if err := validateCommonOptional(fields); err != nil {
		return fmt.Errorf("list: %w", err)
	}
	return nil
}

// Cards 产出一张逐项揭示的卡。
func (listType) Cards(note Note) []Card {
	return []Card{{Template: "forward", Ordinal: 0, Fields: note.Fields}}
}

// Render 正面只给 prompt；背面在 prompt 之外逐项给出 items（Extra）。
func (listType) Render(card Card, side Side) (RenderResult, error) {
	if card.Template != "forward" {
		return RenderResult{}, errUnknownTemplate(card.Template)
	}
	prompt, err := stringField(card.Fields, "prompt")
	if err != nil {
		return RenderResult{}, fmt.Errorf("list: %w", err)
	}
	items, err := stringSliceField(card.Fields, "items")
	if err != nil {
		return RenderResult{}, fmt.Errorf("list: %w", err)
	}
	if side == SideFront {
		return RenderResult{Body: prompt}, nil
	}
	return RenderResult{Body: prompt, Extra: items}, nil
}
