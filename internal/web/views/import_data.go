package views

// 卡组包导入页（M5-9）的渲染数据。
// 与其它页面一致：所有用户可见文案由 handler 从语言包取好后传入，模板不含硬编码文字。

// ImportOption 是导入表单下拉的一项。
type ImportOption struct {
	Value    string
	Label    string
	Selected bool
}

// ImportRow 是导入摘要里的一行标签/值。
type ImportRow struct {
	Label string
	Value string
}

// ImportErrorRow 是导入报告里的一条逐条错误。
type ImportErrorRow struct {
	Entry  string
	Reason string
}

// ImportResult 是导入报告的展示形态；字段与 store.PackageImportReport 一一对应。
type ImportResult struct {
	Heading    string
	Rows       []ImportRow
	Errors     []ImportErrorRow
	ErrorsHead string
	NoErrors   string
	ColEntry   string
	ColReason  string
}

// ImportPageData 是上传页（import.templ）的全部渲染数据。
type ImportPageData struct {
	Layout     LayoutData
	Heading    string
	Intro      string
	FormAction string
	CSRF       string

	FileLabel        string
	TargetLabel      string
	Targets          []ImportOption
	DeckIDLabel      string
	DryRunLabel      string
	OnConflictLabel  string
	OnConflicts      []ImportOption
	AllowOthersLabel string
	AllowOthersShow  bool
	SubmitLabel      string

	Result       *ImportResult
	ErrorMessage string
}
