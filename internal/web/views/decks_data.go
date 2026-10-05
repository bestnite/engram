package views

// DeckRow 是卡组列表页的一行数据。
type DeckRow struct {
	// IDValue 是十进制 id，供链接使用。
	IDValue string
	Name    string
	// Href 指向该卡组的卡片列表页。
	Href      string
	CardCount int64
	// NewCount / ReviewCount 是该卡组单卡组口径下今日可刷的新卡数与可复习数
	//（schedule.DeckCounts，与点进 /review 的条数一致）。两者相加＝实际能刷的张数。
	NewCount    int64
	ReviewCount int64
	Archived    bool
	// ExportHref 指向该卡组的 .edeck 导出下载（M5-9）。
	ExportHref string
	// SharingHref 指向该卡组的共享管理页（M5-2）；仅当当前用户是 owner 时非空，
	// 共享给他人（非 owner）的行留空，模板据此不渲染入口，避免点进去吃 403。
	SharingHref string
	// SettingsHref 指向该卡组的设置页；仅 owner 非空，非 owner 留空不渲染。
	SettingsHref string
	// ReviewHref 指向只复习该卡组的复习页（M3-13）。
	ReviewHref string
}

// DeckOption 是新建表单里的预设下拉项（类型别名兼容 views.SelectOption）。
type DeckOption = SelectOption

// DeckListData 是卡组列表与新建页（M2-11）的全部渲染数据；所有用户可见字符串已本地化。
type DeckListData struct {
	Layout  LayoutData
	Heading string
	// 列表。
	ColName  string
	ColCards string
	ColDue   string
	// NewCardsLabel / ReviewCardsLabel 是「今日」列里两个数字的标签（新 X · 复习 Y）。
	NewCardsLabel    string
	ReviewCardsLabel string
	EmptyText        string
	Archived         string
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
	// SettingsLabel 是 owner 行「设置」入口的文案。
	SettingsLabel string
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
// 之所以放在 Go 常量而非模板内联 <style>：模板只做排版、不内联样式块，CSS 选择器与模板文案
// 规则混在一起也难维护；渲染产物仍是对话框内的 <style>。
const noscriptDialogStyle = `<style>#new-deck-dialog{display:block;position:static;width:auto;max-width:none;margin:0;padding:0;border:0;background:transparent}#new-deck-dialog [data-dialog-close]{display:none}</style>`
