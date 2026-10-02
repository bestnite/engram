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
	// ExportHref 指向该卡组的 .edeck 导出下载（M5-9）。
	ExportHref string
	// SharingHref 指向该卡组的共享管理页（M5-2）；仅当当前用户是 owner 时非空，
	// 共享给他人（非 owner）的行留空，模板据此不渲染入口，避免点进去吃 403。
	SharingHref string
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
	// SharingLabel 是 owner 行「共享」入口的文案（M5-2）。
	SharingLabel string
	// ImportLabel / ImportHref 是列表头部的卡组包导入入口（M5-9）。
	ImportLabel string
	ImportHref  string
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
