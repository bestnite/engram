package cardtype

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// typedType 是输入作答卡：用户键入文本，机器判分。
// 字段：prompt / answer（必填），accept[] / ignore_case / ignore_whitespace / regex（可选），
// extra / source_url（可选）。
type typedType struct{}

// Label 返回语言包键名。
func (typedType) Label() string { return "cardtype.typed" }

// Describe 自描述：正面 prompt、背面 answer；作答控件是文本，判分由 Grader 提供。
func (typedType) Describe() Description {
	return Description{
		Kind:          "typed",
		LabelKey:      "cardtype.typed",
		AnswerControl: AnswerText,
		FrontField:    "prompt",
		BackField:     "answer",
		PromptField:   "prompt",
		Fields: fieldsWithCommon(
			FieldSpec{Key: "prompt", Control: ControlTextarea, Required: true},
			FieldSpec{Key: "answer", Control: ControlText, Required: true},
			FieldSpec{Key: "accept", Control: ControlLines},
			// 服务端默认忽略大小写与空白差异（typedType 的 boolField 第三参是 true）。
			FieldSpec{Key: "ignore_case", Control: ControlBool, Default: true},
			FieldSpec{Key: "ignore_whitespace", Control: ControlBool, Default: true},
		),
		Example: map[string]any{
			"prompt":            "Which element has the chemical symbol Au?",
			"answer":            "gold",
			"accept":            []any{"aurum"},
			"ignore_case":       true,
			"ignore_whitespace": true,
		},
	}
}

// typedSettings 是一次判分要用到的容差规则。
type typedSettings struct {
	answer           string
	accept           []string
	ignoreCase       bool
	ignoreWhitespace bool
	regex            *regexp.Regexp
}

// Validate 校验 prompt / answer 与容差规则；regex 无效时点名该字段。
func (typedType) Validate(fields map[string]any) error {
	if _, err := stringField(fields, "prompt"); err != nil {
		return fmt.Errorf("typed: %w", err)
	}
	if _, err := stringField(fields, "answer"); err != nil {
		return fmt.Errorf("typed: %w", err)
	}
	if _, err := optionalStringSliceField(fields, "accept"); err != nil {
		return fmt.Errorf("typed: %w", err)
	}
	if _, err := boolField(fields, "ignore_case", true); err != nil {
		return fmt.Errorf("typed: %w", err)
	}
	if _, err := boolField(fields, "ignore_whitespace", true); err != nil {
		return fmt.Errorf("typed: %w", err)
	}
	if _, err := typedRegex(fields); err != nil {
		return fmt.Errorf("typed: %w", err)
	}
	if err := validateCommonOptional(fields); err != nil {
		return fmt.Errorf("typed: %w", err)
	}
	return nil
}

// Cards 产出一张正向卡。
func (typedType) Cards(note Note) []Card {
	return []Card{{Template: "forward", Ordinal: 0, Fields: note.Fields}}
}

// Render 正面给 prompt；背面给标准答案，其它可接受答案放进 Extra。
func (typedType) Render(card Card, side Side) (RenderResult, error) {
	if card.Template != "forward" {
		return RenderResult{}, errUnknownTemplate(card.Template)
	}
	prompt, err := stringField(card.Fields, "prompt")
	if err != nil {
		return RenderResult{}, fmt.Errorf("typed: %w", err)
	}
	if side == SideFront {
		return RenderResult{Body: prompt}, nil
	}
	answer, err := stringField(card.Fields, "answer")
	if err != nil {
		return RenderResult{}, fmt.Errorf("typed: %w", err)
	}
	accept, err := optionalStringSliceField(card.Fields, "accept")
	if err != nil {
		return RenderResult{}, fmt.Errorf("typed: %w", err)
	}
	return RenderResult{Body: answer, Extra: accept}, nil
}

// TypedInput 是 typedType.Grade 的输入：用户键入的原始文本加所属 note 字段与映射。
type TypedInput struct {
	GradeContext
	Answer string
}

// ParseAnswer 解码作答：字符串；没有作答按空串判分（记全错），不拒绝。
func (typedType) ParseAnswer(gc GradeContext, raw json.RawMessage) (any, error) {
	s, err := answerString(raw, "typed")
	if err != nil {
		return nil, err
	}
	return TypedInput{GradeContext: gc, Answer: s}, nil
}

// GivenText 返回用户输入的原文。
func (typedType) GivenText(_ map[string]any, detail map[string]any) string {
	given, _ := detail["given"].(string)
	return given
}

// Grade 判分：正则优先于字面比较；字面比较按 ignore_case / ignore_whitespace 归一化后
// 依次比对 answer 与 accept[]。命中记 1 分，否则 0 分，再按映射得到评分档位。
func (typedType) Grade(input any) (int, map[string]any, bool) {
	in, ok := input.(TypedInput)
	if !ok {
		return 0, nil, false
	}
	settings, err := parseTypedSettings(in.Fields)
	if err != nil {
		return 0, nil, false
	}
	given := normalizeTyped(in.Answer, settings.ignoreCase, settings.ignoreWhitespace)
	detail := map[string]any{"answer": settings.answer, "given": in.Answer}

	if settings.regex != nil {
		matched := settings.regex.MatchString(given)
		detail["matched"] = "regex"
		if !matched {
			return in.rating(0, detail)
		}
		return in.rating(1, detail)
	}

	for i, candidate := range settings.accept {
		if normalizeTyped(candidate, settings.ignoreCase, settings.ignoreWhitespace) == given {
			detail["matched"] = fmt.Sprintf("accept[%d]", i)
			return in.rating(1, detail)
		}
	}
	if normalizeTyped(settings.answer, settings.ignoreCase, settings.ignoreWhitespace) == given {
		detail["matched"] = "answer"
		return in.rating(1, detail)
	}
	return in.rating(0, detail)
}

// PromptContexter 为未来 LLM 评分提供题目与参考答案。
func (typedType) PromptContext(note Note) PromptContext {
	prompt, _ := stringField(note.Fields, "prompt")
	answer, _ := stringField(note.Fields, "answer")
	return PromptContext{Question: prompt, Reference: answer}
}

// parseTypedSettings 读取并校验判分规则。
func parseTypedSettings(fields map[string]any) (typedSettings, error) {
	answer, err := stringField(fields, "answer")
	if err != nil {
		return typedSettings{}, err
	}
	accept, err := optionalStringSliceField(fields, "accept")
	if err != nil {
		return typedSettings{}, err
	}
	ignoreCase, err := boolField(fields, "ignore_case", true)
	if err != nil {
		return typedSettings{}, err
	}
	ignoreWhitespace, err := boolField(fields, "ignore_whitespace", true)
	if err != nil {
		return typedSettings{}, err
	}
	re, err := typedRegex(fields)
	if err != nil {
		return typedSettings{}, err
	}
	return typedSettings{answer: answer, accept: accept, ignoreCase: ignoreCase, ignoreWhitespace: ignoreWhitespace, regex: re}, nil
}

// typedRegex 解析可选 regex 字段；ignore_case 生效时加 (?i) 前缀，并要求整串匹配。
func typedRegex(fields map[string]any) (*regexp.Regexp, error) {
	v, ok := fields["regex"]
	if !ok || v == nil {
		return nil, nil
	}
	pattern, ok := v.(string)
	if !ok || pattern == "" {
		return nil, fmt.Errorf(`field "regex" must be a non-empty string`)
	}
	ignoreCase, err := boolField(fields, "ignore_case", true)
	if err != nil {
		return nil, err
	}
	if ignoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(`\A(?:` + pattern + `)\z`)
	if err != nil {
		return nil, fmt.Errorf(`field "regex" is not a valid regular expression: %w`, err)
	}
	return re, nil
}

// normalizeTyped 按规则归一化待比较文本：ignore_whitespace 时折叠空白并去首尾，
// 否则保持原样（不裁剪）；可选忽略大小写。
func normalizeTyped(s string, ignoreCase, ignoreWhitespace bool) string {
	if ignoreWhitespace {
		s = strings.Join(strings.Fields(s), " ")
	}
	if ignoreCase {
		s = strings.ToLower(s)
	}
	return s
}

// optionalStringSliceField 读取可选字符串数组；缺失返回 nil，空数组也接受（accept 可以为空）。
func optionalStringSliceField(fields map[string]any, name string) ([]string, error) {
	v, ok := fields[name]
	if !ok || v == nil {
		return nil, nil
	}
	switch ss := v.(type) {
	case []string:
		return ss, nil
	case []any:
		out := make([]string, len(ss))
		for i, item := range ss {
			s, ok := item.(string)
			if !ok || s == "" {
				return nil, fmt.Errorf("field %q[%d] must be a non-empty string", name, i)
			}
			out[i] = s
		}
		return out, nil
	default:
		return nil, fmt.Errorf("field %q must be an array of strings", name)
	}
}
