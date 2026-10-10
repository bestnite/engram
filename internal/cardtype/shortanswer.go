package cardtype

import (
	"encoding/json"
	"fmt"
	"strings"
)

// shortAnswerType 是主观自由文本卡：prompt 提问，学习者先写下自己的答案，再与可选的
// reference 参考答案对照后自评（「主观类」：先自评，LLM 评分留待后续）。
//
// 先写再对照是它与 basic 的区别：把答案写出来比在脑子里过一遍更能检验是否真的记住。
// 写下的作答随自评一起存进复习记录（见 RecordAnswer），LLM 判分接入时判的就是这段文字。
//
// 字段：prompt（必填）、reference（可选参考答案）、extra / source_url（可选）。
// 本题型故意不实现 Grader：它不在机器判分集合内，复习流程对它走自评路径；
// LLM 判分会在本题型补齐 PromptContexter / Grader 时再接入，届时核心管线不变。
type shortAnswerType struct{}

// Label 返回语言包键名。
func (shortAnswerType) Label() string { return "cardtype.short_answer" }

// Describe 自描述：正面 prompt、背面 reference；作答控件是自由书写，写完仍由学习者自评
// （不实现 Grader）。
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

// Cards 产出一张正向卡（冻结表：生成 card = 1）。
func (shortAnswerType) Cards(note Note) []Card {
	return []Card{{Template: "forward", Ordinal: 0, Fields: note.Fields}}
}

// Render 正面显示 prompt；背面只给参考答案，不重复题干（复习页把背面接在正面下方）。
// 没有参考答案时背面为空，复习页只对照学习者自己写下的作答。
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

// RecordAnswer 解码学习者自评前写下的作答（字符串），返回随复习记录保存的细节。
// 没写（空作答或只有空白）返回 nil：不存一条空作答。
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

// optionalStringField 读取可选字符串字段：缺失或 nil 返回空串，存在时必须是非空字符串。
// 与 stringField 分开，避免把「可选的参考答案」误判成必填（冻结表）。
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
