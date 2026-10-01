package views

// 管理面板（M6-1、M6-5）的渲染数据。
// 与其它页面一致：所有用户可见文案由 handler 从语言包取好后传入，模板不含硬编码文字。

// AdminNavItem 是管理面板侧边导航的一项。
// Implemented 为 false 时模板渲染成置灰文本而不是链接：这些子页（用户管理、审计等）
// 在后续任务里实现前不能注册路由，否则点进去会 404 跳出面板。
type AdminNavItem struct {
	Label       string
	Href        string
	Active      bool
	Implemented bool
	// PendingLabel 是未实现子页旁的「未实现」标记文案。
	PendingLabel string
}

// AdminColumns 是设置表的三列表头（设置 / 当前值 / 来源）。
type AdminColumns struct {
	Setting string
	Value   string
	Source  string
}

// SettingRow 是设置表里的一行。
type SettingRow struct {
	Label string
	// Key 同时是表单字段名，提交后用于写 settings 表。
	Key string
	// Value 是非敏感设置的当前生效值（敏感行留空，绝不回显明文）。
	Value string
	// StatusText 是敏感行的「已配置 / 未配置」文案。
	StatusText string
	// SourceLabel 说明生效值来自环境变量、数据库还是默认值。
	SourceLabel string
	Hint        string
	// Sensitive 为 true 时只显示配置状态并提供一个空密码框写入新值。
	Sensitive bool
	// Editable 为 true 时模板渲染输入框。
	Editable bool
}

// AdminSection 是一组设置（通用 / 媒体 / 敏感配置）。
type AdminSection struct {
	Heading string
	Intro   string
	Rows    []SettingRow
}

// AdminPageData 是管理面板外壳（admin.templ）的渲染数据。
type AdminPageData struct {
	Layout     LayoutData
	Heading    string
	Intro      string
	NavHeading string
	Nav        []AdminNavItem
	Columns    AdminColumns
	Sections   []AdminSection
	// Notice 是一条已本地化的操作结果提示；为空时不渲染。
	Notice string
	// ShowForm 为 true 时把设置区块包在写表单里（仅系统设置页）。
	ShowForm   bool
	FormAction string
	CSRF       string
	SaveLabel  string
	// 全库导出入口（GET，无需 CSRF）。
	ExportHeading string
	ExportLabel   string
	ExportHref    string
	ExportHint    string
}
