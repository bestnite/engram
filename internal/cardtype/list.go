package cardtype

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// listType 是列表卡：给出 prompt，学习者逐项写出 items，由服务端逐项判分。
// 字段：prompt（必填）、items[]（必填、非空）、ordered（可选 bool，缺省 false），
// extra / source_url（可选）。
//
// 列表卡要考的是「记住了几项」，自评只能给整体印象，所以按想起的条数给部分分。
type listType struct{}

// Label 返回语言包键名。
func (listType) Label() string { return "cardtype.list" }

// Describe 自描述：正面 prompt、背面 items；作答控件是逐项输入（每项一个文本框）。
func (listType) Describe() Description {
	return Description{
		Kind:          "list",
		LabelKey:      "cardtype.list",
		AnswerControl: AnswerItems,
		FrontField:    "prompt",
		BackField:     "items",
		PromptField:   "prompt",
		Fields: fieldsWithCommon(
			FieldSpec{Key: "prompt", Control: ControlTextarea, Required: true},
			FieldSpec{Key: "items", Control: ControlLines, Required: true},
			// ordered 的服务端默认是 false（cardtype.listType 的 boolField 第三参）。
			FieldSpec{Key: "ordered", Control: ControlBool, Default: false},
		),
		Example: map[string]any{
			"prompt":  "Name the additive primary colours of light.",
			"items":   []any{"red", "green", "blue|blue-violet"},
			"ordered": false,
		},
	}
}

// Validate 校验 prompt / items / ordered；条目里用 | 分隔的备选答案不能有空段。
func (listType) Validate(fields map[string]any) error {
	if _, err := stringField(fields, "prompt"); err != nil {
		return fmt.Errorf("list: %w", err)
	}
	items, err := stringSliceField(fields, "items")
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	for i, item := range items {
		for _, alt := range splitAlternatives(item, false) {
			if strings.TrimSpace(alt) == "" {
				return fmt.Errorf("list: field %q[%d] has an empty answer alternative around \"|\"", "items", i)
			}
		}
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

// Cards 产出一张卡。
func (listType) Cards(note Note) []Card {
	return []Card{{Template: "forward", Ordinal: 0, Fields: note.Fields}}
}

// Render 正面只给 prompt；背面只给条目列表，不重复题干（复习页把背面接在正面下方）。
// ordered 决定背面是编号列表还是圆点列表；一项有多个可接受的写法时全部列出。
func (listType) Render(card Card, side Side) (RenderResult, error) {
	if card.Template != "forward" {
		return RenderResult{}, errUnknownTemplate(card.Template)
	}
	prompt, items, ordered, err := listParts(card.Fields)
	if err != nil {
		return RenderResult{}, err
	}
	if side == SideFront {
		return RenderResult{Body: prompt}, nil
	}
	var b strings.Builder
	for i, item := range items {
		if ordered {
			fmt.Fprintf(&b, "%d. ", i+1)
		} else {
			b.WriteString("- ")
		}
		b.WriteString(strings.Join(clozeAlternatives(item, false), " / "))
		b.WriteString("\n")
	}
	return RenderResult{Body: b.String()}, nil
}

// ListInput 是 listType.Grade 的输入：Answers 是学习者逐项写下的条目（顺序即输入框顺序）。
type ListInput struct {
	GradeContext
	Answers []string
}

// ParseAnswer 解码作答：字符串数组；没有作答等同于一项都没写（判为全错）。
func (listType) ParseAnswer(gc GradeContext, raw json.RawMessage) (any, error) {
	answers := []string{}
	if !emptyAnswer(raw) {
		if err := json.Unmarshal(raw, &answers); err != nil {
			return nil, errors.New("list answer must be an array of strings, one per item")
		}
	}
	return ListInput{GradeContext: gc, Answers: answers}, nil
}

// GivenText 返回学习者写下的非空条目，以逗号连接。
func (listType) GivenText(_ map[string]any, detail map[string]any) string {
	given, _ := detail["given"].([]string)
	written := make([]string, 0, len(given))
	for _, g := range given {
		if strings.TrimSpace(g) != "" {
			written = append(written, g)
		}
	}
	return strings.Join(written, ", ")
}

// Grade 逐项判分：score = 想起的条数 / 总条数。写错的条目只是不得分，不倒扣，
// 鼓励学习者把想到的都写出来。
//
//   - ordered=false：顺序不限，每条作答最多抵一项、每项最多被抵一次。用二分图最大匹配
//     而不是贪心，否则备选答案互相重叠时（「蓝|紫」与「紫」）先到先得会少算。
//   - ordered=true：第 i 个作答只和第 i 项比对，位置错了不算。
//
// 比对规则与挖空题一致（见 matchesAlternatives）。作答多于条数视为无法判分。
func (listType) Grade(input any) (int, map[string]any, bool) {
	in, ok := input.(ListInput)
	if !ok {
		return 0, nil, false
	}
	_, items, ordered, err := listParts(in.Fields)
	if err != nil || len(in.Answers) > len(items) {
		return 0, nil, false
	}
	given := make([]string, len(items))
	copy(given, in.Answers)
	alts := make([][]string, len(items))
	for i, item := range items {
		alts[i] = clozeAlternatives(item, false)
	}
	correct := make([]bool, len(items))
	if ordered {
		for i := range items {
			correct[i] = matchesAlternatives(given[i], alts[i])
		}
	} else {
		correct = matchUnordered(given, alts)
	}
	hits := 0
	for _, c := range correct {
		if c {
			hits++
		}
	}
	detail := map[string]any{"items": alts, "given": given, "correct": correct, "ordered": ordered}
	return in.rating(float64(hits)/float64(len(items)), detail)
}

// Breakdown 把判分细节还原成逐项结果：按条目顺序给出每项的写法与是否想起。
func (listType) Breakdown(detail map[string]any) []BreakdownItem {
	alts, _ := detail["items"].([][]string)
	correct, _ := detail["correct"].([]bool)
	if len(alts) != len(correct) {
		return nil
	}
	out := make([]BreakdownItem, len(alts))
	for i := range alts {
		out[i] = BreakdownItem{Answer: strings.Join(alts[i], " / "), Correct: correct[i]}
	}
	return out
}

// BlankHints 让复习页给出与条数相同的输入框；列表条目没有提示，全部为空串。
func (listType) BlankHints(card Card) []string {
	items, err := stringSliceField(card.Fields, "items")
	if err != nil {
		return nil
	}
	return make([]string, len(items))
}

// matchUnordered 求作答与条目的最大匹配，返回每个条目是否被某条作答抵上。
// 条目数是个位到几十，增广路算法足够，不必引入更复杂的匹配实现。
func matchUnordered(given []string, alts [][]string) []bool {
	// edges[g] 列出第 g 条作答能抵的条目。
	edges := make([][]int, len(given))
	for g, answer := range given {
		for i := range alts {
			if matchesAlternatives(answer, alts[i]) {
				edges[g] = append(edges[g], i)
			}
		}
	}
	owner := make([]int, len(alts))
	for i := range owner {
		owner[i] = -1
	}
	var augment func(g int, seen []bool) bool
	augment = func(g int, seen []bool) bool {
		for _, i := range edges[g] {
			if seen[i] {
				continue
			}
			seen[i] = true
			if owner[i] < 0 || augment(owner[i], seen) {
				owner[i] = g
				return true
			}
		}
		return false
	}
	for g := range given {
		augment(g, make([]bool, len(alts)))
	}
	correct := make([]bool, len(alts))
	for i, g := range owner {
		correct[i] = g >= 0
	}
	return correct
}

// listParts 读取并校验列表卡的三要素。
func listParts(fields map[string]any) (string, []string, bool, error) {
	prompt, err := stringField(fields, "prompt")
	if err != nil {
		return "", nil, false, fmt.Errorf("list: %w", err)
	}
	items, err := stringSliceField(fields, "items")
	if err != nil {
		return "", nil, false, fmt.Errorf("list: %w", err)
	}
	ordered, err := boolField(fields, "ordered", false)
	if err != nil {
		return "", nil, false, fmt.Errorf("list: %w", err)
	}
	return prompt, items, ordered, nil
}
