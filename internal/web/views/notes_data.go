package views

import "github.com/a-h/templ"

// rawHTML 把已清洗的 HTML 片段作为组件嵌入页面。
// 片段一定来自 internal/render（已经过 bluemonday 白名单清洗），因此这里可以安全地
// 跳过模板默认转义；把它包成组件是为了让模板只需写 @rawHTML(...)，不引入额外导入。
func rawHTML(s string) templ.Component { return templ.Raw(s) }

// KindOption 是题型/状态筛选下拉里的一项（类型别名兼容 views.SelectOption）。

type PreviewCard struct {
	TemplateLabel string
	FrontHTML     string
	BackHTML      string
}

// NotePreviewData 是预览片段（编辑页内嵌 + htmx 局部刷新）的渲染数据。
type NotePreviewData struct {
	Title      string
	FrontLabel string
	BackLabel  string
	Error      string
	Cards      []PreviewCard
}
