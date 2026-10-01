package views

import "github.com/a-h/templ"

// rawHTML 把已清洗的 HTML 片段作为组件嵌入页面。
// 片段一定来自 internal/render（已经过 bluemonday 白名单清洗），因此这里可以安全地
// 跳过模板默认转义；把它包成组件是为了让模板只需写 @rawHTML(...)，不引入额外导入。
func rawHTML(s string) templ.Component { return templ.Raw(s) }

// KindOption 是题型/状态筛选下拉里的一项；Selected 决定是否带 selected 属性。
type KindOption struct {
	Value    string
	Label    string
	Selected bool
}

// NoteRow 是卡片列表的一行。
type NoteRow struct {
	// IDValue 是复选框 value 与编辑链接用的十进制 id。
	IDValue   string
	Kind      string
	KindLabel string
	Front     string
	Tags      []string
	Created   string
	// Deleted 为 true 时该行处于软删除状态。
	Deleted     bool
	StatusLabel string
	EditHref    string
}

// Pagination 是列表底部的分页数据；href 为空时对应按钮不渲染。
type Pagination struct {
	PrevLabel string
	NextLabel string
	PrevHref  string
	NextHref  string
	PageLabel string
}

// NoteListData 是卡片列表页（M2-7）的全部渲染数据；每个用户可见字符串都已本地化。
type NoteListData struct {
	Layout   LayoutData
	Heading  string
	DeckName string
	// FilterAction 是筛选表单的 action（保留当前卡组路径）。
	FilterAction      string
	SearchLabel       string
	SearchPlaceholder string
	SearchValue       string
	TagLabel          string
	TagPlaceholder    string
	TagValue          string
	KindLabel         string
	KindValue         string
	StatusLabel       string
	StatusValue       string
	FilterSubmit      string
	FilterReset       string
	ResetHref         string
	// Kinds / Statuses 是下拉选项；AllLabel 是“全部”选项的文案（值为空串）。
	Kinds      []KindOption
	Statuses   []KindOption
	ColKind    string
	ColFront   string
	ColTags    string
	ColCreated string
	ColStatus  string
	Rows       []NoteRow
	EmptyText  string
	TotalLabel string
	PageLabel  string
	Pagination Pagination
	// 批量操作表单。
	BulkAction         string
	BulkHint           string
	BulkDeleteLabel    string
	BulkTagLabel       string
	BulkTagPlaceholder string
	BulkSubmitLabel    string
	// NewNoteLabel/NewNoteHref 指向新建卡片页（M2-12）。
	NewNoteLabel string
	NewNoteHref  string
	// Redirect 是批量操作完成后要跳回的列表地址（含筛选与页码）。
	Redirect string
	CSRF     string
}

// FieldInput 是编辑页上的一个字段输入框；Value 是可直接编辑的字符串形式。
type FieldInput struct {
	Name  string
	Label string
	Value string
}

// CreateField 是新建卡片表单上的一个输入控件；Input 决定控件形态（text/textarea/number/checkbox/lines）。
type CreateField struct {
	Name    string
	Label   string
	Input   string
	Value   string
	Checked bool
	Hint    string
}

// NewNoteData 是新建卡片页（M2-12）的渲染数据。
type NewNoteData struct {
	Layout       LayoutData
	Heading      string
	KindLabel    string
	Kinds        []KindOption
	Fields       []CreateField
	FieldsURL    string
	PreviewURL   string
	Action       string
	SaveLabel    string
	BackLabel    string
	BackHref     string
	CSRF         string
	ErrorMessage string
	Preview      NotePreviewData
}

// NoteEditData 是卡片编辑页（M2-7）的渲染数据。
type NoteEditData struct {
	Layout        LayoutData
	Heading       string
	KindLabel     string
	Fields        []FieldInput
	Action        string
	SaveLabel     string
	PreviewLabel  string
	PreviewURL    string
	BackLabel     string
	BackHref      string
	NoteID        string
	CSRF          string
	ErrorMessage  string
	Deleted       bool
	DeletedNotice string
	Preview       NotePreviewData
}

// PreviewCard 是一张卡渲染后的正反面 HTML 片段。
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
