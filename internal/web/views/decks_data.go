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
	// ReviewHref 指向只复习该卡组的复习页（M3-13）。
	ReviewHref string
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
	// ReviewLabel / ReviewSelectedLabel / SelectLabel 是多卡组复习
	// 入口的文案（M3-13）：每行的「复习」、页头「复习所选」与复选框可访问名称。
	ReviewLabel         string
	ReviewSelectedLabel string
	SelectLabel         string
	// SharingLabel 是 owner 行「共享」入口的文案（M5-2）。
	SharingLabel string
	// ImportLabel / ImportHref 是列表头部的卡组包导入入口（M5-9）。
	ImportLabel string
	ImportHref  string
	Rows        []DeckRow
	// 新建表单。
	NewHeading string
	// CloseLabel 是新建卡组对话框关闭按钮的可访问名称（M8-7）。
	CloseLabel   string
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

// noscriptDialogStyle 让新建卡组对话框在禁用 JavaScript 时静态展开、可直接填写（M8-7）。
// 之所以放在 Go 常量而非模板内联 <style>：no-template-literals 检查把模板里任何非空字母串
// 都当作硬编码用户文案，CSS 选择器会被误判；渲染产物仍是对话框内的 <style>。
const noscriptDialogStyle = `<style>#new-deck-dialog{display:block;position:static;width:auto;max-width:none;margin:0;padding:0;border:0;background:transparent}#new-deck-dialog [data-dialog-close]{display:none}</style>`
