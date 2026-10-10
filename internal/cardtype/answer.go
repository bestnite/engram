package cardtype

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// 作答类题型的判分入口（服务层只调 GradeAnswer，不按题型分支）。

// ErrUngradable 表示作答能解码，但判分器认为无法判分（例如选项索引越界）。
var ErrUngradable = errors.New("cardtype: answer cannot be graded")

// 判分结论，与前端反馈的三种判定一一对应。
const (
	VerdictCorrect   = "correct"
	VerdictPartial   = "partial"
	VerdictIncorrect = "incorrect"
)

// GradeOutcome 是一次服务端判分的结果。
type GradeOutcome struct {
	// Rating 是映射后的 1–4 评分档位。
	Rating int
	// Score 是 [0,1] 的得分。
	Score float64
	// Verdict 是 correct / partial / incorrect。
	Verdict string
	// Given 是展示用的作答文本（见 Grader.GivenText）。
	Given string
	// Detail 是判分细节，原样写入 reviews.grade_detail_json。
	Detail map[string]any
}

// BreakdownItem 是逐项判分结果里的一项：Answer 是该项可接受的写法（Markdown 原文），
// Correct 表示学习者是否答对了这一项。
type BreakdownItem struct {
	Answer  string
	Correct bool
}

// GradeAnswer 用题型的判分器解码并判分一次作答。
// 解码失败返回该错误（调用方映射成 400）；能解码但无法判分返回 ErrUngradable。
func GradeAnswer(g Grader, gc GradeContext, raw json.RawMessage) (GradeOutcome, error) {
	input, err := g.ParseAnswer(gc, raw)
	if err != nil {
		return GradeOutcome{}, err
	}
	rating, detail, ok := g.Grade(input)
	if !ok {
		return GradeOutcome{}, ErrUngradable
	}
	score, _ := detail["score"].(float64)
	m := DefaultGradeMapping()
	if gc.Mapping != nil {
		m = *gc.Mapping
	}
	return GradeOutcome{
		Rating:  rating,
		Score:   score,
		Verdict: m.Verdict(score),
		Given:   g.GivenText(gc.Fields, detail),
		Detail:  detail,
	}, nil
}

// Verdict 按映射阈值把得分归为全对 / 部分对 / 全错，与 RatingFor 用同一组阈值。
func (m GradeMapping) Verdict(score float64) string {
	n := m.withDefaults()
	switch {
	case score >= n.FullThreshold:
		return VerdictCorrect
	case score > n.NoneThreshold:
		return VerdictPartial
	default:
		return VerdictIncorrect
	}
}

// emptyAnswer 报告 raw 是否表示「没有作答」（空或 JSON null）。
func emptyAnswer(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || string(t) == "null"
}

// answerString 解码字符串作答；空作答得到空串。
func answerString(raw json.RawMessage, kind string) (string, error) {
	if emptyAnswer(raw) {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("%s answer must be a string", kind)
	}
	return s, nil
}

// answerIndex 解码单个 0 基选项索引：接受 JSON 整数，也接受内容是整数的字符串。
func answerIndex(raw json.RawMessage, kind string) (int, error) {
	if emptyAnswer(raw) {
		return 0, fmt.Errorf("%s answer is required", kind)
	}
	var idx int
	if err := json.Unmarshal(raw, &idx); err == nil {
		return idx, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			return n, nil
		}
	}
	return 0, fmt.Errorf("%s answer must be an option index", kind)
}

// optionText 返回第 idx 个选项的文本；越界或字段异常时返回空串。
func optionText(fields map[string]any, idx int) string {
	opts, err := stringSliceField(fields, "options")
	if err != nil || idx < 0 || idx >= len(opts) {
		return ""
	}
	return opts[idx]
}
