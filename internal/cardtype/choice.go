package cardtype

import (
	"encoding/json"
	"fmt"
	"strings"
)

// 本文件实现两种选择卡：
//   choice_single: question / options[]（≥2，去重）/ answer（0 基索引）
//   choice_multi : question / options[]（≥2，去重）/ answers[]（0 基索引，≥1，去重）
// options 与答案索引的约束 JSON Schema 表达不了，由 Validate 在此处保证并点名字段。

// choiceSingleType 是单选卡。
type choiceSingleType struct{}

// Label 返回语言包键名。
func (choiceSingleType) Label() string { return "cardtype.choice_single" }

// Describe 自描述：正面 question、背面 options；作答控件是单选（取 options 下标）。
func (choiceSingleType) Describe() Description {
	return Description{
		Kind:          "choice_single",
		LabelKey:      "cardtype.choice_single",
		AnswerControl: AnswerSingle,
		FrontField:    "question",
		BackField:     "options",
		PromptField:   "question",
		OptionsField:  "options",
		Fields: fieldsWithCommon(
			FieldSpec{Key: "question", Control: ControlTextarea, Required: true},
			FieldSpec{Key: "options", Control: ControlLines, Required: true},
			FieldSpec{Key: "answer", Control: ControlIndex, Required: true},
		),
		Example: map[string]any{
			"question": "Which planet is closest to the Sun?",
			"options":  []any{"Venus", "Mercury", "Mars"},
			"answer":   1,
		},
	}
}

// Validate 校验 question / options / answer，并保证索引落在 options 范围内。
func (choiceSingleType) Validate(fields map[string]any) error {
	if _, err := stringField(fields, "question"); err != nil {
		return fmt.Errorf("choice_single: %w", err)
	}
	options, err := stringSliceField(fields, "options")
	if err != nil {
		return fmt.Errorf("choice_single: %w", err)
	}
	if len(options) < 2 {
		return fmt.Errorf("choice_single: field %q needs at least 2 options", "options")
	}
	if !uniqueStrings(options) {
		return fmt.Errorf("choice_single: field %q must not contain duplicates", "options")
	}
	answer, err := intField(fields, "answer")
	if err != nil {
		return fmt.Errorf("choice_single: %w", err)
	}
	if answer >= len(options) {
		return fmt.Errorf("choice_single: field %q index %d is out of range for %d options", "answer", answer, len(options))
	}
	if err := validateCommonOptional(fields); err != nil {
		return fmt.Errorf("choice_single: %w", err)
	}
	return nil
}

// Cards 产出一张正向卡。
func (choiceSingleType) Cards(note Note) []Card {
	return []Card{{Template: "forward", Ordinal: 0, Fields: note.Fields}}
}

// Render 正面给问题与全部选项；背面只给正确选项的文本。
func (choiceSingleType) Render(card Card, side Side) (RenderResult, error) {
	if card.Template != "forward" {
		return RenderResult{}, errUnknownTemplate(card.Template)
	}
	question, options, answer, err := choiceSingleParts(card.Fields)
	if err != nil {
		return RenderResult{}, err
	}
	if side == SideFront {
		return RenderResult{Body: question, Extra: options}, nil
	}
	return RenderResult{Body: options[answer]}, nil
}

// ChoiceSingleInput 是 choiceSingleType.Grade 的输入；Selected 是 0 基选项索引。
type ChoiceSingleInput struct {
	GradeContext
	Selected int
}

// ParseAnswer 解码作答：一个 0 基选项索引（整数，或内容为整数的字符串）；没有作答返回错误。
func (choiceSingleType) ParseAnswer(gc GradeContext, raw json.RawMessage) (any, error) {
	idx, err := answerIndex(raw, "choice_single")
	if err != nil {
		return nil, err
	}
	return ChoiceSingleInput{GradeContext: gc, Selected: idx}, nil
}

// GivenText 返回被选中选项的文本。
func (choiceSingleType) GivenText(fields map[string]any, detail map[string]any) string {
	idx, ok := detail["selected"].(int)
	if !ok {
		return ""
	}
	return optionText(fields, idx)
}

// Grade 判分：选中正确选项记 1 分，选中其它合法选项记 0 分；索引越界视为无法判分。
func (choiceSingleType) Grade(input any) (int, map[string]any, bool) {
	in, ok := input.(ChoiceSingleInput)
	if !ok {
		return 0, nil, false
	}
	_, options, answer, err := choiceSingleParts(in.Fields)
	if err != nil {
		return 0, nil, false
	}
	if in.Selected < 0 || in.Selected >= len(options) {
		return 0, nil, false
	}
	detail := map[string]any{"answer": answer, "selected": in.Selected}
	if in.Selected == answer {
		return in.rating(1, detail)
	}
	return in.rating(0, detail)
}

// PromptContexter 为未来 LLM 评分提供题目与参考答案。
func (choiceSingleType) PromptContext(note Note) PromptContext {
	question, options, answer, err := choiceSingleParts(note.Fields)
	if err != nil {
		return PromptContext{}
	}
	return PromptContext{Question: question + "\n" + strings.Join(options, "\n"), Reference: options[answer]}
}

// choiceSingleParts 读取并校验单选卡的三要素。
func choiceSingleParts(fields map[string]any) (string, []string, int, error) {
	question, err := stringField(fields, "question")
	if err != nil {
		return "", nil, 0, fmt.Errorf("choice_single: %w", err)
	}
	options, err := stringSliceField(fields, "options")
	if err != nil {
		return "", nil, 0, fmt.Errorf("choice_single: %w", err)
	}
	answer, err := intField(fields, "answer")
	if err != nil {
		return "", nil, 0, fmt.Errorf("choice_single: %w", err)
	}
	if answer >= len(options) {
		return "", nil, 0, fmt.Errorf("choice_single: field %q index %d is out of range for %d options", "answer", answer, len(options))
	}
	return question, options, answer, nil
}

// choiceMultiType 是多选卡。
type choiceMultiType struct{}

// Label 返回语言包键名。
func (choiceMultiType) Label() string { return "cardtype.choice_multi" }

// Describe 自描述：正面 question、背面 options；作答控件是多选（取 options 下标集合）。
func (choiceMultiType) Describe() Description {
	return Description{
		Kind:          "choice_multi",
		LabelKey:      "cardtype.choice_multi",
		AnswerControl: AnswerMulti,
		FrontField:    "question",
		BackField:     "options",
		PromptField:   "question",
		OptionsField:  "options",
		Fields: fieldsWithCommon(
			FieldSpec{Key: "question", Control: ControlTextarea, Required: true},
			FieldSpec{Key: "options", Control: ControlLines, Required: true},
			FieldSpec{Key: "answers", Control: ControlIndexes, Required: true},
		),
		Example: map[string]any{
			"question": "Which of these numbers are prime?",
			"options":  []any{"2", "4", "5", "9"},
			"answers":  []any{0, 2},
		},
	}
}

// Validate 校验 question / options / answers，并保证索引在范围内、无重复。
func (choiceMultiType) Validate(fields map[string]any) error {
	if _, err := stringField(fields, "question"); err != nil {
		return fmt.Errorf("choice_multi: %w", err)
	}
	options, err := stringSliceField(fields, "options")
	if err != nil {
		return fmt.Errorf("choice_multi: %w", err)
	}
	if len(options) < 2 {
		return fmt.Errorf("choice_multi: field %q needs at least 2 options", "options")
	}
	if !uniqueStrings(options) {
		return fmt.Errorf("choice_multi: field %q must not contain duplicates", "options")
	}
	answers, err := intSliceField(fields, "answers")
	if err != nil {
		return fmt.Errorf("choice_multi: %w", err)
	}
	if !uniqueInts(answers) {
		return fmt.Errorf("choice_multi: field %q must not contain duplicates", "answers")
	}
	for _, idx := range answers {
		if idx >= len(options) {
			return fmt.Errorf("choice_multi: field %q index %d is out of range for %d options", "answers", idx, len(options))
		}
	}
	if err := validateCommonOptional(fields); err != nil {
		return fmt.Errorf("choice_multi: %w", err)
	}
	return nil
}

// Cards 产出一张正向卡。
func (choiceMultiType) Cards(note Note) []Card {
	return []Card{{Template: "forward", Ordinal: 0, Fields: note.Fields}}
}

// Render 正面给问题与全部选项；背面按顺序列出所有正确选项。
func (choiceMultiType) Render(card Card, side Side) (RenderResult, error) {
	if card.Template != "forward" {
		return RenderResult{}, errUnknownTemplate(card.Template)
	}
	question, options, answers, err := choiceMultiParts(card.Fields)
	if err != nil {
		return RenderResult{}, err
	}
	if side == SideFront {
		return RenderResult{Body: question, Extra: options}, nil
	}
	correct := make([]string, 0, len(answers))
	for _, idx := range answers {
		correct = append(correct, options[idx])
	}
	return RenderResult{Body: strings.Join(correct, ", ")}, nil
}

// ChoiceMultiInput 是 choiceMultiType.Grade 的输入；Selected 是 0 基选项索引集合。
type ChoiceMultiInput struct {
	GradeContext
	Selected []int
}

// ParseAnswer 解码作答：0 基选项索引数组；没有作答等同于一个都没选（合法的错误答案）。
func (choiceMultiType) ParseAnswer(gc GradeContext, raw json.RawMessage) (any, error) {
	selected := []int{}
	if !emptyAnswer(raw) {
		if err := json.Unmarshal(raw, &selected); err != nil {
			return nil, fmt.Errorf("choice_multi answer must be an array of option indices")
		}
	}
	return ChoiceMultiInput{GradeContext: gc, Selected: selected}, nil
}

// GivenText 返回被选中选项的文本，以逗号连接。
func (choiceMultiType) GivenText(fields map[string]any, detail map[string]any) string {
	idxs, _ := detail["selected"].([]int)
	picked := make([]string, 0, len(idxs))
	for _, idx := range idxs {
		if text := optionText(fields, idx); text != "" {
			picked = append(picked, text)
		}
	}
	return strings.Join(picked, ", ")
}

// Grade 判分并给出部分得分：score = max(0, 命中数 - 误选数) / 正确数，上限 1。
// 这样「选中全部正确项」= 1 分（Good），「只选中一部分」= 部分分（默认 Hard），
// 「全错」= 0 分（Again），满足部分得分的默认映射。
// 任一选中索引越界视为无法判分。
func (choiceMultiType) Grade(input any) (int, map[string]any, bool) {
	in, ok := input.(ChoiceMultiInput)
	if !ok {
		return 0, nil, false
	}
	_, options, answers, err := choiceMultiParts(in.Fields)
	if err != nil {
		return 0, nil, false
	}
	correct := make(map[int]bool, len(answers))
	for _, idx := range answers {
		correct[idx] = true
	}
	selected := map[int]bool{}
	for _, idx := range in.Selected {
		if idx < 0 || idx >= len(options) {
			return 0, nil, false
		}
		selected[idx] = true
	}
	hits, misses := 0, 0
	for idx := range selected {
		if correct[idx] {
			hits++
		} else {
			misses++
		}
	}
	score := float64(hits-misses) / float64(len(answers))
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	detail := map[string]any{"answers": answers, "selected": in.Selected, "hits": hits, "misses": misses}
	return in.rating(score, detail)
}

// PromptContexter 为未来 LLM 评分提供题目与参考答案。
func (choiceMultiType) PromptContext(note Note) PromptContext {
	question, options, answers, err := choiceMultiParts(note.Fields)
	if err != nil {
		return PromptContext{}
	}
	correct := make([]string, 0, len(answers))
	for _, idx := range answers {
		correct = append(correct, options[idx])
	}
	return PromptContext{Question: question + "\n" + strings.Join(options, "\n"), Reference: strings.Join(correct, ", ")}
}

// choiceMultiParts 读取并校验多选卡的三要素。
func choiceMultiParts(fields map[string]any) (string, []string, []int, error) {
	question, err := stringField(fields, "question")
	if err != nil {
		return "", nil, nil, fmt.Errorf("choice_multi: %w", err)
	}
	options, err := stringSliceField(fields, "options")
	if err != nil {
		return "", nil, nil, fmt.Errorf("choice_multi: %w", err)
	}
	answers, err := intSliceField(fields, "answers")
	if err != nil {
		return "", nil, nil, fmt.Errorf("choice_multi: %w", err)
	}
	for _, idx := range answers {
		if idx >= len(options) {
			return "", nil, nil, fmt.Errorf("choice_multi: field %q index %d is out of range for %d options", "answers", idx, len(options))
		}
	}
	return question, options, answers, nil
}

// uniqueStrings 判断字符串数组是否无重复。
func uniqueStrings(ss []string) bool {
	seen := make(map[string]bool, len(ss))
	for _, s := range ss {
		if seen[s] {
			return false
		}
		seen[s] = true
	}
	return true
}

// uniqueInts 判断整数数组是否无重复。
func uniqueInts(is []int) bool {
	seen := make(map[int]bool, len(is))
	for _, i := range is {
		if seen[i] {
			return false
		}
		seen[i] = true
	}
	return true
}
