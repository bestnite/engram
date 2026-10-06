package views

import "github.com/a-h/templ"

// rawHTML 把已清洗的 HTML 片段作为组件嵌入页面。
// 片段一定来自 internal/render（已经过 bluemonday 白名单清洗），因此这里可以安全地
// 跳过模板默认转义；把它包成组件是为了让模板只需写 @rawHTML(...)，不引入额外导入。
func rawHTML(s string) templ.Component { return templ.Raw(s) }

// KindOption 是题型/状态筛选下拉里的一项（类型别名兼容 views.SelectOption）。
type KindOption = SelectOption

// NoteKindAttrs 返回新建卡片页面中题型下拉联动所需的 htmx 属性。
func NoteKindAttrs(fieldsURL string) templ.Attributes {
	return templ.Attributes{
		"hx-get":     fieldsURL,
		"hx-target":  "#new-note-fields",
		"hx-include": "closest form",
		"hx-trigger": "change",
	}
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
	// CloneAction/CloneLabel 是把当前卡组克隆到自己账号的入口（M5-4，reader 及以上可用）。
	CloneAction string
	CloneLabel  string
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

// MediaUploadData 是编辑器里的媒体上传控件数据（M2-9）。所有用户可见文案都来自语言包；
// 上传失败时前端按稳定英文 code 选择对应文案。Disabled 为 true 时（存储未装配）不渲染控件。
type MediaUploadData struct {
	Disabled    bool
	URL         string
	Accept      string
	Label       string
	Button      string
	Hint        string
	Inserted    string
	ErrTooLarge string
	ErrMime     string
	ErrMagic    string
	ErrGeneric  string
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
	Upload       MediaUploadData
	Picker       MediaPickerData
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
	Upload        MediaUploadData
	Picker        MediaPickerData
}

// MediaPickerData 是编辑页上「从媒体库选择」入口的数据（选择器面板）。
// Disabled 为 true 时（媒体存储或 service 未装配）不渲染入口。
type MediaPickerData struct {
	Disabled  bool
	OpenLabel string
	URL       string
}

// MediaPickerListID 是选择器片段的根元素 id：入口与翻页都以此为目标做 htmx 局部替换。
const MediaPickerListID = "media-picker-list"

// MediaPickerItem 是选择器里的一项：缩略预览走原图（不生成缩略图，DESIGN.md §6.3 原则），
// 由 CSS 限尺寸 + loading="lazy" 承担；点击把 InsertURL 以 Markdown 图片语法插入聚焦字段。
type MediaPickerItem struct {
	Sha256    string
	Src       string
	InsertURL string
}

// MediaPickerFragmentData 是选择器片段（htmx 返回，不含整页外壳）的渲染数据。
type MediaPickerFragmentData struct {
	Heading   string
	Empty     string
	NextLabel string
	NextURL   string
	Items     []MediaPickerItem
}

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
