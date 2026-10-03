package views

import (
	"strings"

	"github.com/a-h/templ"
)

// SelectOption 是自定义下拉菜单中的通用选项。
type SelectOption struct {
	Value    string
	Label    string
	Selected bool
}

// SelectProps 是 Select 组件的入参配置。
type SelectProps struct {
	ID          string           // 元素唯一 ID（关联 label 与测试）
	Name        string           // 表单字段名
	Options     []SelectOption   // 选项列表
	Placeholder string           // 未选中时的占位提示（来自语言包）
	Class       string           // 外部包装容器的附加 class
	Small       bool             // 紧凑小尺寸模式
	Attrs       templ.Attributes // 透传 HTML 属性（如 hx-get, hx-target 等）
}

// selectInitialOption 返回当前选中的选项，若无选中则返回列表首项。
func selectInitialOption(options []SelectOption) SelectOption {
	for _, opt := range options {
		if opt.Selected {
			return opt
		}
	}
	if len(options) > 0 {
		return options[0]
	}
	return SelectOption{}
}

// selectInitialValue 计算隐藏输入框的初始值。
func selectInitialValue(props SelectProps) string {
	return selectInitialOption(props.Options).Value
}

// selectDisplayLabel 计算下拉框初始展示文本。
func selectDisplayLabel(props SelectProps) string {
	initial := selectInitialOption(props.Options)
	if initial.Label != "" {
		return initial.Label
	}
	return props.Placeholder
}

// selectWrapperClass 合并基础包装类与调用方传入的自定义 class。
// 若调用方显式指定了宽度（如 w-32、w-auto 等），则不再附加默认的 w-full，避免样式冲突。
func selectWrapperClass(extra string) string {
	if extra == "" {
		return "relative inline-block w-full"
	}
	for _, part := range strings.Fields(extra) {
		if strings.HasPrefix(part, "w-") {
			return "relative inline-block " + extra
		}
	}
	return "relative inline-block w-full " + extra
}
