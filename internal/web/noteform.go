package web

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/web/views"
)

// 本文件实现 M2-12 新建卡片表单的字段定义与表单值转换。
//
// 字段名严格取自 DESIGN.md §6.2 的冻结表；字段的必填/约束仍由 cardtype.Validate
// 权威判定（NoteStore.Create 内部调用），这里的 spec 只决定表单渲染什么输入控件，
// 不复制校验逻辑。表单控件类型与字段值类型必须对得上（数组写 []any、索引写 float64），
// 否则 cardtype 会以"字段类型错误"拒绝——这正是我们要暴露给用户的信息。

// 表单输入形态。
const (
	inputText     = "text"
	inputTextarea = "textarea"
	inputNumber   = "number"
	inputCheckbox = "checkbox"
	inputLines    = "lines" // 每行一项的文本域，也接受 JSON 数组
)

// 数组元素类型（仅 inputLines 用）。
const (
	elemString = "string"
	elemNumber = "number"
)

// createFieldSpec 描述新建表单上的一个字段。
type createFieldSpec struct {
	name     string // 字段名，与冻结表一致
	input    string // 输入形态，见上方常量
	elem     string // lines 的元素类型
	required bool   // 仅用于布尔字段：是否无论是否勾选都要提交（true_false.answer 必填）
	hintKey  string // 显示在输入框下的提示文案键；空串不显示
}

// createFieldSpecs 是每种题型在新建表单上的字段顺序与控件形态（DESIGN.md §6.2）。
// extra（对象）暂不放进新建表单，用户可在编辑页补充。
var createFieldSpecs = map[string][]createFieldSpec{
	"basic": {
		{name: "front", input: inputText, required: true},
		{name: "back", input: inputTextarea, required: true},
		{name: "source_url", input: inputText},
	},
	"basic_both": {
		{name: "front", input: inputText, required: true},
		{name: "back", input: inputTextarea, required: true},
		{name: "source_url", input: inputText},
	},
	"cloze": {
		{name: "text", input: inputTextarea, required: true},
		{name: "source_url", input: inputText},
	},
	"list": {
		{name: "prompt", input: inputText, required: true},
		{name: "items", input: inputLines, elem: elemString, required: true, hintKey: "notes.create.lines_hint"},
		{name: "ordered", input: inputCheckbox},
	},
	"typed": {
		{name: "prompt", input: inputText, required: true},
		{name: "answer", input: inputText, required: true},
		{name: "accept", input: inputLines, elem: elemString, hintKey: "notes.create.lines_hint"},
		{name: "ignore_case", input: inputCheckbox},
		{name: "ignore_whitespace", input: inputCheckbox},
		{name: "regex", input: inputCheckbox},
	},
	"numeric": {
		{name: "prompt", input: inputText, required: true},
		{name: "value", input: inputNumber, required: true},
		{name: "tolerance_absolute", input: inputNumber},
		{name: "tolerance_relative", input: inputNumber},
		{name: "unit", input: inputText},
	},
	"choice_single": {
		{name: "question", input: inputTextarea, required: true},
		{name: "options", input: inputLines, elem: elemString, required: true, hintKey: "notes.create.lines_hint"},
		{name: "answer", input: inputNumber, required: true, hintKey: "notes.create.number_hint"},
	},
	"choice_multi": {
		{name: "question", input: inputTextarea, required: true},
		{name: "options", input: inputLines, elem: elemString, required: true, hintKey: "notes.create.lines_hint"},
		{name: "answers", input: inputLines, elem: elemNumber, required: true, hintKey: "notes.create.lines_hint"},
	},
	"true_false": {
		{name: "statement", input: inputTextarea, required: true},
		{name: "answer", input: inputCheckbox, required: true},
	},
	"short_answer": {
		{name: "prompt", input: inputText, required: true},
		{name: "reference", input: inputTextarea},
	},
}

// createFieldSpecsFor 返回题型的新建字段定义；未知题型返回 nil。
func createFieldSpecsFor(kind string) []createFieldSpec {
	return createFieldSpecs[strings.TrimSpace(kind)]
}

// createFields 把题型字段定义渲染成表单控件数据；values 为回显的原始表单值（可为 nil）。
func (s *Server) createFields(loc *i18n.Localizer, kind string, values map[string]string) []views.CreateField {
	specs := createFieldSpecsFor(kind)
	out := make([]views.CreateField, 0, len(specs))
	for _, sp := range specs {
		field := views.CreateField{
			Name:  sp.name,
			Label: s.fieldLabel(loc, sp.name),
			Input: sp.input,
		}
		if sp.hintKey != "" {
			field.Hint = loc.T(sp.hintKey)
		}
		if values != nil {
			if sp.input == inputCheckbox {
				field.Checked = strings.TrimSpace(values[sp.name]) != ""
			} else {
				field.Value = values[sp.name]
			}
		}
		out = append(out, field)
	}
	return out
}

// rawCreateValues 取出表单里某题型各字段的原始文本，用于校验失败时回显。
func rawCreateValues(c *gin.Context, kind string) map[string]string {
	specs := createFieldSpecsFor(kind)
	out := make(map[string]string, len(specs))
	for _, sp := range specs {
		out[sp.name] = c.PostForm("field." + sp.name)
	}
	return out
}

// collectCreateFields 把表单值转换成 cardtype 期望的类型：字符串原样、数字 float64、
// 数组 []any、布尔 bool。空字符串一律省略（让"必填缺失"由校验报错而不是落成空值）。
func collectCreateFields(c *gin.Context, kind string) map[string]any {
	specs := createFieldSpecsFor(kind)
	out := make(map[string]any, len(specs))
	for _, sp := range specs {
		if sp.input == inputCheckbox {
			checked := strings.TrimSpace(c.PostForm("field."+sp.name)) != ""
			// 可选布尔字段省略即取默认值；必填布尔字段（true_false.answer）始终提交。
			if checked || sp.required {
				out[sp.name] = checked
			}
			continue
		}
		raw := strings.TrimSpace(c.PostForm("field." + sp.name))
		if raw == "" {
			continue
		}
		switch sp.input {
		case inputNumber:
			if f, err := strconv.ParseFloat(raw, 64); err == nil {
				out[sp.name] = f
			} else {
				// 非法数字原样交给校验器，让它报出"必须是数字"并点名字段。
				out[sp.name] = raw
			}
		case inputLines:
			out[sp.name] = parseListInput(raw, sp.elem)
		default:
			out[sp.name] = raw
		}
	}
	return out
}

// parseListInput 解析数组字段：优先按 JSON 数组；否则按行拆分，逐行转成字符串或数字索引。
func parseListInput(raw, elem string) any {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "[") {
		var decoded []any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
			return decoded
		}
	}
	lines := strings.Split(raw, "\n")
	out := make([]any, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if elem == elemNumber {
			if f, err := strconv.ParseFloat(line, 64); err == nil {
				out = append(out, f)
				continue
			}
		}
		out = append(out, line)
	}
	return out
}
