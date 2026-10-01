package views

// RoleOption 是角色下拉里的一项；Selected 决定是否带 selected 属性。
type RoleOption struct {
	Value    string
	Label    string
	Selected bool
}

// SharingRow 是共享管理页里的一行授权。
// Editable 为 false 时（目前只有 owner 行）只展示角色、不渲染改角色/撤销表单。
type SharingRow struct {
	UserValue string
	Username  string
	RoleValue string
	RoleLabel string
	Editable  bool
	// RoleOptions 是该行的「改角色」下拉选项（不含 owner）。
	RoleOptions []RoleOption
	// RoleAction / RevokeAction 是该行的表单 action。
	RoleAction   string
	RevokeAction string
	ChangeLabel  string
	RevokeLabel  string
}

// SharingData 是卡组共享管理页（M5-2）的全部渲染数据；所有用户可见字符串已本地化。
type SharingData struct {
	Layout     LayoutData
	Heading    string
	DeckName   string
	ColUser    string
	ColRole    string
	ColActions string
	OwnerNote  string
	Rows       []SharingRow
	EmptyText  string
	// 授权表单。
	GrantHeading        string
	GrantAction         string
	UsernameLabel       string
	UsernamePlaceholder string
	RoleLabel           string
	RoleOptions         []RoleOption
	GrantSubmit         string
	ErrorMessage        string
	// CSRF 是全部写表单需要的会话绑定 token（DESIGN.md §4.3）。
	CSRF string
}
