package cardtype

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// numericType 是数值作答卡：用户键入数值，按绝对/相对容差判分。
// 字段：prompt / value（必填），tolerance_absolute / tolerance_relative / unit（可选），
// extra / source_url（可选）。这套判分直接服务「分数↔百分数」这类纯记忆映射内容。
type numericType struct{}

// Label 返回语言包键名。
func (numericType) Label() string { return "cardtype.numeric" }

// Describe 自描述：正面 prompt、背面 value；作答控件是数值，判分由 Grader 提供。
func (numericType) Describe() Description {
	return Description{
		Kind:          "numeric",
		LabelKey:      "cardtype.numeric",
		AnswerControl: AnswerNumber,
		FrontField:    "prompt",
		BackField:     "value",
		PromptField:   "prompt",
		Fields: fieldsWithCommon(
			FieldSpec{Key: "prompt", Control: ControlTextarea, Required: true},
			FieldSpec{Key: "value", Control: ControlNumber, Required: true},
			FieldSpec{Key: "unit", Control: ControlText},
			FieldSpec{Key: "tolerance_absolute", Control: ControlNumber},
			FieldSpec{Key: "tolerance_relative", Control: ControlNumber},
		),
		Example: map[string]any{
			"prompt":             "What is standard gravity \\(g_0\\)?",
			"value":              9.80665,
			"unit":               "m/s²",
			"tolerance_relative": 0.01,
		},
	}
}

// Validate 校验 prompt / value 与容差规则；相对容差是 0–1 的比例。
func (numericType) Validate(fields map[string]any) error {
	if _, err := stringField(fields, "prompt"); err != nil {
		return fmt.Errorf("numeric: %w", err)
	}
	if _, err := numberField(fields, "value"); err != nil {
		return fmt.Errorf("numeric: %w", err)
	}
	if tol, err := optionalFloatField(fields, "tolerance_absolute"); err != nil {
		return fmt.Errorf("numeric: %w", err)
	} else if tol != nil && *tol < 0 {
		return fmt.Errorf("numeric: field %q must not be negative", "tolerance_absolute")
	}
	if tol, err := optionalFloatField(fields, "tolerance_relative"); err != nil {
		return fmt.Errorf("numeric: %w", err)
	} else if tol != nil && (*tol < 0 || *tol > 1) {
		return fmt.Errorf("numeric: field %q must be between 0 and 1", "tolerance_relative")
	}
	if v, ok := fields["unit"]; ok && v != nil {
		if s, ok := v.(string); !ok || s == "" {
			return fmt.Errorf(`numeric: field "unit" must be a non-empty string`)
		}
	}
	if err := validateCommonOptional(fields); err != nil {
		return fmt.Errorf("numeric: %w", err)
	}
	return nil
}

// Cards 产出一张正向卡。
func (numericType) Cards(note Note) []Card {
	return []Card{{Template: "forward", Ordinal: 0, Fields: note.Fields}}
}

// Render 正面给 prompt；背面给数值，设置 unit 时追加单位。
func (numericType) Render(card Card, side Side) (RenderResult, error) {
	if card.Template != "forward" {
		return RenderResult{}, errUnknownTemplate(card.Template)
	}
	prompt, err := stringField(card.Fields, "prompt")
	if err != nil {
		return RenderResult{}, fmt.Errorf("numeric: %w", err)
	}
	if side == SideFront {
		return RenderResult{Body: prompt}, nil
	}
	value, err := numberField(card.Fields, "value")
	if err != nil {
		return RenderResult{}, fmt.Errorf("numeric: %w", err)
	}
	body := formatNumber(value)
	if unit, ok := card.Fields["unit"].(string); ok && unit != "" {
		body += " " + unit
	}
	return RenderResult{Body: body}, nil
}

// NumericInput 是 numericType.Grade 的输入。
type NumericInput struct {
	GradeContext
	Answer string
}

// ParseAnswer 解码作答：通常是字符串（可能带单位），也接受裸 JSON 数字并保留原文；
// 没有作答按空串判分。
func (numericType) ParseAnswer(gc GradeContext, raw json.RawMessage) (any, error) {
	if emptyAnswer(raw) {
		return NumericInput{GradeContext: gc}, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return NumericInput{GradeContext: gc, Answer: s}, nil
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return nil, fmt.Errorf("numeric answer must be a string or number")
	}
	return NumericInput{GradeContext: gc, Answer: n.String()}, nil
}

// GivenText 返回用户输入的原文。
func (numericType) GivenText(_ map[string]any, detail map[string]any) string {
	given, _ := detail["given"].(string)
	return given
}

// Grade 判分：|answer - value| 不超过绝对容差与相对容差（相对值是 |value| 的比例）中的较大者
// 即算正确。答案里可带配置的 unit 后缀，解析前会剥掉再比较。
func (numericType) Grade(input any) (int, map[string]any, bool) {
	in, ok := input.(NumericInput)
	if !ok {
		return 0, nil, false
	}
	value, err := numberField(in.Fields, "value")
	if err != nil {
		return 0, nil, false
	}
	absTol := 0.0
	if t, err := optionalFloatField(in.Fields, "tolerance_absolute"); err != nil {
		return 0, nil, false
	} else if t != nil {
		absTol = *t
	}
	relTol := 0.0
	if t, err := optionalFloatField(in.Fields, "tolerance_relative"); err != nil {
		return 0, nil, false
	} else if t != nil {
		relTol = *t
	}
	unit, _ := in.Fields["unit"].(string)

	detail := map[string]any{"value": value, "given": in.Answer}
	if absTol > 0 {
		detail["tolerance_absolute"] = absTol
	}
	if relTol > 0 {
		detail["tolerance_relative"] = relTol
	}
	if unit != "" {
		detail["unit"] = unit
	}

	answer, parseOK := parseNumericAnswer(in.Answer, unit)
	if !parseOK {
		detail["parsed"] = false
		return in.rating(0, detail)
	}
	detail["parsed_answer"] = answer

	allowed := absTol
	if rel := relTol * math.Abs(value); rel > allowed {
		allowed = rel
	}
	if math.Abs(answer-value) <= allowed {
		return in.rating(1, detail)
	}
	return in.rating(0, detail)
}

// PromptContexter 为未来 LLM 评分提供题目与参考答案。
func (numericType) PromptContext(note Note) PromptContext {
	prompt, _ := stringField(note.Fields, "prompt")
	value, err := numberField(note.Fields, "value")
	reference := ""
	if err == nil {
		reference = formatNumber(value)
		if unit, ok := note.Fields["unit"].(string); ok && unit != "" {
			reference += " " + unit
		}
	}
	return PromptContext{Question: prompt, Reference: reference}
}

// parseNumericAnswer 把用户输入解析为数值：去首尾空白，剥掉可选的 unit 后缀。
// 返回 false 表示输入不是可比较的数值（按错误处理，计入 Again）。
func parseNumericAnswer(raw, unit string) (float64, bool) {
	s := strings.TrimSpace(raw)
	if unit != "" && strings.HasSuffix(strings.ToLower(s), strings.ToLower(unit)) {
		s = strings.TrimSpace(s[:len(s)-len(unit)])
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}
