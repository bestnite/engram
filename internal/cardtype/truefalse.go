package cardtype

import (
	"fmt"
	"strconv"
)

// trueFalseType 是判断卡：用户判定 statement 的真假（DESIGN.md §6.2）。
// 字段：statement / answer（bool，必填），extra / source_url（可选）。
type trueFalseType struct{}

// Label 返回语言包键名。
func (trueFalseType) Label() string { return "cardtype.true_false" }

// Validate 校验 statement 与布尔 answer。
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

// Cards 产出一张正向卡。
func (trueFalseType) Cards(note Note) []Card {
	return []Card{{Template: "forward", Ordinal: 0, Fields: note.Fields}}
}

// Render 正面给陈述句；背面给出真假。真假是内容本身，本地化由视图层负责（DESIGN.md §8.3）。
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

// TrueFalseInput 是 trueFalseType.Grade 的输入；Answer 为 nil 表示未作答，无法判分。
type TrueFalseInput struct {
	GradeContext
	Answer *bool
}

// Grade 判分：判断正确记 1 分，错误记 0 分。
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

// PromptContexter 为未来 LLM 评分提供题目与参考答案（DESIGN.md §14.4）。
func (trueFalseType) PromptContext(note Note) PromptContext {
	statement, _ := stringField(note.Fields, "statement")
	answer, _ := requiredBoolField(note.Fields, "answer")
	return PromptContext{Question: statement, Reference: strconv.FormatBool(answer)}
}
