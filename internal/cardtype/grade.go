package cardtype

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
)

// 本文件承载作答类题型的判分骨架：分数→评分档位映射、判分输入基类，
// 以及 fields 的数值/整数读取辅助。六个具体题型在 typed.go / numeric.go /
// choice.go / truefalse.go / cloze.go 中实现。

// FSRS 四档评分，与 reviews.rating 的整数约定一致。
const (
	RatingAgain = 1
	RatingHard  = 2
	RatingGood  = 3
	RatingEasy  = 4
)

// GradeMapping 是「分数→评分档位」映射，以 JSON 存进 preset。
//
// 分数是 [0,1] 的正确率：1 = 全对，0 = 全错，其余为部分得分。默认映射是
// 全对 = Good、部分对 = Hard、全错 = Again。把它放进 preset
// 是为了按题型/卡组微调，例如把全对提到 Easy，或把部分对降成 Again。
//
// 阈值可选：score >= FullThreshold 判为全对，score <= NoneThreshold 判为全错，
// 中间判为部分得分。阈值为零或负时回退到默认（全对阈值 1.0、全错阈值 0.0）。
type GradeMapping struct {
	Version       int     `json:"version"`
	Full          int     `json:"full"`
	Partial       int     `json:"partial"`
	None          int     `json:"none"`
	FullThreshold float64 `json:"full_threshold,omitempty"`
	NoneThreshold float64 `json:"none_threshold,omitempty"`
}

// GradeMappingVersion 是本包认识的映射结构版本；未知版本拒绝，避免静默误读。
const GradeMappingVersion = 1

// DefaultGradeMapping 返回默认映射：全对 Good、部分 Hard、全错 Again。
func DefaultGradeMapping() GradeMapping {
	return GradeMapping{
		Version:       GradeMappingVersion,
		Full:          RatingGood,
		Partial:       RatingHard,
		None:          RatingAgain,
		FullThreshold: 1.0,
		NoneThreshold: 0.0,
	}
}

// ParseGradeMapping 解析 preset 里的映射 JSON。空串（列为 NULL）回退到默认映射，
// 这是「缺省回退」的唯一入口。
func ParseGradeMapping(raw string) (GradeMapping, error) {
	m := DefaultGradeMapping()
	if raw == "" {
		return m, nil
	}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return GradeMapping{}, fmt.Errorf("grade mapping: invalid JSON: %w", err)
	}
	if err := m.validate(); err != nil {
		return GradeMapping{}, err
	}
	return m.withDefaults(), nil
}

// MarshalGradeMapping 把映射编码为存库用的 JSON 文本。
func MarshalGradeMapping(m GradeMapping) (string, error) {
	if err := m.validate(); err != nil {
		return "", err
	}
	b, err := json.Marshal(m.withDefaults())
	if err != nil {
		return "", fmt.Errorf("grade mapping: marshal: %w", err)
	}
	return string(b), nil
}

// withDefaults 补齐未设置（零值）的评分与阈值。
func (m GradeMapping) withDefaults() GradeMapping {
	d := DefaultGradeMapping()
	if m.Version == 0 {
		m.Version = d.Version
	}
	if m.Full == 0 {
		m.Full = d.Full
	}
	if m.Partial == 0 {
		m.Partial = d.Partial
	}
	if m.None == 0 {
		m.None = d.None
	}
	if m.FullThreshold <= 0 {
		m.FullThreshold = d.FullThreshold
	}
	if m.NoneThreshold <= 0 {
		m.NoneThreshold = d.NoneThreshold
	}
	return m
}

// validate 校验评分取值与阈值次序；错误点名字段，便于调用方定位。
func (m GradeMapping) validate() error {
	if m.Version != 0 && m.Version != GradeMappingVersion {
		return fmt.Errorf("grade mapping: unsupported version %d", m.Version)
	}
	n := m.withDefaults()
	for name, rating := range map[string]int{"full": n.Full, "partial": n.Partial, "none": n.None} {
		if rating < RatingAgain || rating > RatingEasy {
			return fmt.Errorf("grade mapping: %s rating %d out of range 1-4", name, rating)
		}
	}
	if n.FullThreshold <= n.NoneThreshold {
		return fmt.Errorf("grade mapping: full_threshold (%g) must be greater than none_threshold (%g)", n.FullThreshold, n.NoneThreshold)
	}
	return nil
}

// RatingFor 把 [0,1] 的分数映射为 1–4 的评分档位。分数越界时夹到 [0,1]。
func (m GradeMapping) RatingFor(score float64) int {
	n := m.withDefaults()
	if math.IsNaN(score) {
		return n.None
	}
	if score = math.Max(0, math.Min(1, score)); score >= n.FullThreshold {
		return n.Full
	}
	if score <= n.NoneThreshold {
		return n.None
	}
	return n.Partial
}

// GradeContext 是作答类判分输入的公共部分：Fields 是 note 的字段（判分所需的
// 正确答案、选项、容差规则都在里面），Mapping 是该题所属 preset 的映射（nil = 默认）。
//
// 判分器只拿到 input，因此字段必须随 input 传入；这样可选窄接口 Grader 无需扩张，
// 核心提交管线也不必改动。
//
// Template 是被作答那张卡的模板（例如 cloze:2）。同一 note 的不同卡考的内容不同时
// （挖空题每个序号一张卡），判分器靠它找到本卡的考点；其它题型一卡一 note，可以忽略。
type GradeContext struct {
	Fields   map[string]any
	Template string
	Mapping  *GradeMapping
}

// rating 用映射把分数转成评分并填充 detail 的公共字段。
func (c GradeContext) rating(score float64, detail map[string]any) (int, map[string]any, bool) {
	m := DefaultGradeMapping()
	if c.Mapping != nil {
		m = *c.Mapping
	}
	r := m.RatingFor(score)
	if detail == nil {
		detail = map[string]any{}
	}
	detail["score"] = score
	detail["rating"] = r
	return r, detail, true
}

// numberField 读取必填数值字段；兼容 JSON 反序列化的 float64 与 Go 侧构造的整数类型。
func numberField(fields map[string]any, name string) (float64, error) {
	v, ok := fields[name]
	if !ok || v == nil {
		return 0, fmt.Errorf("missing required field %q", name)
	}
	f, ok := toFloat(v)
	if !ok {
		return 0, fmt.Errorf("field %q must be a number", name)
	}
	return f, nil
}

// optionalFloatField 读取可选数值字段；缺失时返回 (nil, nil)。
func optionalFloatField(fields map[string]any, name string) (*float64, error) {
	v, ok := fields[name]
	if !ok || v == nil {
		return nil, nil
	}
	f, ok := toFloat(v)
	if !ok {
		return nil, fmt.Errorf("field %q must be a number", name)
	}
	return &f, nil
}

// intField 读取必填的 0 基索引字段；必须是整数值（JSON 里是 float64）。
func intField(fields map[string]any, name string) (int, error) {
	v, ok := fields[name]
	if !ok || v == nil {
		return 0, fmt.Errorf("missing required field %q", name)
	}
	return toIndex(v, name)
}

// intSliceField 读取必填的 0 基索引数组（choice_multi 的 answers）。
func intSliceField(fields map[string]any, name string) ([]int, error) {
	v, ok := fields[name]
	if !ok || v == nil {
		return nil, fmt.Errorf("missing required field %q", name)
	}
	raw, ok := v.([]any)
	if !ok {
		if is, ok := v.([]int); ok {
			if len(is) == 0 {
				return nil, fmt.Errorf("field %q must not be empty", name)
			}
			return is, nil
		}
		return nil, fmt.Errorf("field %q must be an array of integers", name)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("field %q must not be empty", name)
	}
	out := make([]int, len(raw))
	for i, item := range raw {
		idx, err := toIndex(item, fmt.Sprintf("%s[%d]", name, i))
		if err != nil {
			return nil, err
		}
		out[i] = idx
	}
	return out, nil
}

// requiredBoolField 读取必填布尔字段；与 boolField 不同，缺失即错误（true_false 的 answer）。
func requiredBoolField(fields map[string]any, name string) (bool, error) {
	v, ok := fields[name]
	if !ok || v == nil {
		return false, fmt.Errorf("missing required field %q", name)
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("field %q must be a boolean", name)
	}
	return b, nil
}

// toFloat 把 JSON/Go 里常见的数值类型统一成 float64。
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// toIndex 把值转成非负整数索引，拒绝小数与非法类型。
func toIndex(v any, name string) (int, error) {
	switch n := v.(type) {
	case int:
		if n < 0 {
			return 0, fmt.Errorf("field %q must be a non-negative integer", name)
		}
		return n, nil
	case int64:
		if n < 0 {
			return 0, fmt.Errorf("field %q must be a non-negative integer", name)
		}
		return int(n), nil
	case float64:
		if n < 0 || n != math.Trunc(n) {
			return 0, fmt.Errorf("field %q must be a non-negative integer", name)
		}
		return int(n), nil
	case json.Number:
		i, err := n.Int64()
		if err != nil || i < 0 {
			return 0, fmt.Errorf("field %q must be a non-negative integer", name)
		}
		return int(i), nil
	default:
		return 0, fmt.Errorf("field %q must be a non-negative integer", name)
	}
}

// formatNumber 以最简形式输出数值，供渲染使用（50 不写成 50.000000）。
func formatNumber(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}
