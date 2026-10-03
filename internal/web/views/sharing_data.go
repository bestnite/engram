package views

// RoleOption 是角色下拉里的一项（类型别名兼容 views.SelectOption）。
// 可见性下拉（M5-5）复用同一结构：Value 是 visibility 取值，Label 是本地化显示名。
type RoleOption = SelectOption

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

// ShareLinkRow 是分享链接区块里的一行（M5-3）。
// TokenValue 是库里存的摘要（sha256 十六进制），只用于撤销表单定位；它不是可用链接，反推不出明文。
type ShareLinkRow struct {
	TokenValue   string
	Prefix       string
	CreatedText  string
	ExpiresText  string
	PasswordText string
	StateText    string
	Active       bool
	RevokeLabel  string
	// RevokeAction 是该行的撤销表单 action。
	RevokeAction string
}

// SharingData 是卡组共享管理页（M5-2、M5-3、M5-5）的全部渲染数据；所有用户可见字符串已本地化。
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

	// M5-3 分享链接区块。
	LinksHeading     string
	LinksEmpty       string
	ColLink          string
	ColPassword      string
	ColExpires       string
	ColState         string
	LinkRows         []ShareLinkRow
	CreateLinkAction string
	PasswordLabel    string
	PasswordHint     string
	ExpiresLabel     string
	ExpiresHint      string
	CreateLinkLabel  string
	// NewLinkURL 仅在创建成功后填充一次，用于“明文只显示一次”。
	NewLinkURL      string
	NewLinkNotice   string
	RevokeAllAction string
	RevokeAllLabel  string

	// M5-5 可见性区块。
	VisibilityHeading string
	VisibilityLabel   string
	VisibilityNote    string
	VisibilityOptions []RoleOption
	VisibilitySubmit  string
	VisibilityAction  string

	// CSRF 是全部写表单需要的会话绑定 token（DESIGN.md §4.3）。
	CSRF string
}
