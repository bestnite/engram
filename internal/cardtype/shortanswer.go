package cardtype

import "fmt"

// shortAnswerType 是主观自由文本卡：prompt 提问，作答后自评，可选 reference 作参考答案
// （DESIGN.md §6.2「主观类（§14）」、§14：先自评，LLM 评分留待后续）。
//
// 字段：prompt（必填）、reference（可选参考答案）、extra / source_url（可选）。
// 本题型故意不实现 Grader：它不在 M3-6 的机器判分集合内，复习流程对它走自评路径；
// LLM 判分（M10-3）会在本题型补齐 PromptContexter / Grader 时再接入，届时核心管线不变。
type shortAnswerType struct{}

// Label 返回语言包键名（DESIGN.md §8.3）。
func (shortAnswerType) Label() string { return "cardtype.short_answer" }

// Validate 校验 prompt / reference。reference 是可选的参考答案，缺省合法。
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

// Cards 产出一张正向卡（DESIGN.md §6.2 冻结表：生成 card = 1）。
func (shortAnswerType) Cards(note Note) []Card {
	return []Card{{Template: "forward", Ordinal: 0, Fields: note.Fields}}
}

// Render 正面显示 prompt；背面在 prompt 之外给出参考答案（有则逐条展示，无则只给 prompt）。
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
	if ref == "" {
		return RenderResult{Body: prompt}, nil
	}
	return RenderResult{Body: prompt, Extra: []string{ref}}, nil
}

// optionalStringField 读取可选字符串字段：缺失或 nil 返回空串，存在时必须是非空字符串。
// 与 stringField 分开，避免把「可选的参考答案」误判成必填（DESIGN.md §6.2 冻结表）。
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
