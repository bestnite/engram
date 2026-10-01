package views

// DeckRow 是卡组列表页的一行数据。
type DeckRow struct {
	// IDValue 是十进制 id，供链接使用。
	IDValue string
	Name    string
	// Href 指向该卡组的卡片列表页。
	Href      string
	CardCount int64
	DueCount  int64
	Archived  bool
	// ExportHref 指向该卡组的 .fdeck 导出下载（M5-9）。
	ExportHref string
}

// DeckOption 是新建表单里的预设下拉项。
type DeckOption struct {
	Value    string
	Label    string
	Selected bool
}

// DeckListData 是卡组列表与新建页（M2-11）的全部渲染数据；所有用户可见字符串已本地化。
type DeckListData struct {
	Layout  LayoutData
	Heading string
	// 列表。
	ColName   string
	ColCards  string
	ColDue    string
	EmptyText string
	Archived  string
	// ColActions/ExportLabel 是导出控件的表头与文案（M5-9）。
	ColActions  string
	ExportLabel string
	Rows        []DeckRow
	// 新建表单。
	NewHeading   string
	NameLabel    string
	NameValue    string
	DescLabel    string
	DescValue    string
	PresetLabel  string
	Presets      []DeckOption
	CreateLabel  string
	ErrorMessage string
	// CSRF 是新建表单需要的会话绑定 token（DESIGN.md §4.3）。
	CSRF string
}
