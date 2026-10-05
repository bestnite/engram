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

// AdminPolicyOption 是注册策略单选的一项。
type AdminPolicyOption struct {
	Value   string
	Label   string
	Checked bool
}

// AdminInviteRow 是注册与邀请页里的一条邀请。
type AdminInviteRow struct {
	ID    string
	Token string
	// Link 是可直接分享的注册链接（/register?invite=<token>）。
	Link        string
	Email       string
	RoleLabel   string
	StatusValue string
	StatusLabel string
	CreatedAt   string
	ExpiresAt   string
	UsedAt      string
	UsedBy      string
	// Active 为 true 时该邀请仍可用，模板才渲染「撤销」入口。
	Active bool
	// RevokeHref 是撤销表单的提交地址，由 handler 拼好。
	RevokeHref string
}

// AdminPageData 是管理面板外壳（admin.templ）的渲染数据。
// AdminDashboardCard 是管理面板概览页的一张计数卡（DESIGN.md §8.4）：
// 整张卡是一个链接，Value 已经由 handler 格式化好（数字或本地化过的组合串）。
type AdminDashboardCard struct {
	Label string
	Value string
	Href  string
}

type AdminPageData struct {
	Layout     LayoutData
	Heading    string
	Intro      string
	NavHeading string
	Nav        []AdminNavItem
	Columns    AdminColumns
	Sections   []AdminSection
	// ---- 概览页（DESIGN.md §8.4）----
	// DashboardPage 为 true 时模板渲染计数卡网格，而不是设置区块或分区页。
	DashboardPage  bool
	DashboardCards []AdminDashboardCard

	// Notice 是一条已本地化的操作结果提示；为空时不渲染。
	Notice string
	// ShowForm 为 true 时把设置区块包在写表单里（仅系统设置页）。
	ShowForm   bool
	FormAction string
	CSRF       string
	SaveLabel  string

	// ---- 用户管理页（M6-2）----
	// UsersPage 为 true 时模板渲染用户表而不是设置区块。
	UsersPage bool
	// SearchQuery/Label/Placeholder/Submit 是搜索表单。
	SearchQuery       string
	SearchLabel       string
	SearchPlaceholder string
	SearchSubmit      string
	// Users 是当前页的用户行；Page/Pages/Total 用于分页导航。
	Users []AdminUserRow
	Page  int
	Pages int
	Total int64
	// Col* 是用户表表头。
	ColUser, ColEmail, ColRole, ColStatus, ColDecks, ColCards, ColReviews, ColActions string
	// Create* 是新建用户表单。
	CreateHeading       string
	CreateUsernameLabel string
	CreateEmailLabel    string
	CreatePasswordLabel string
	CreateRoleLabel     string
	CreateSubmit        string
	// Roles 是角色下拉选项。
	Roles []AdminRoleOption
	// StatusActiveLabel/StatusDisabledLabel 是账号状态显示文案。
	StatusActiveLabel   string
	StatusDisabledLabel string
	// 以下是各动作的按钮/确认文案。
	EnableLabel        string
	DisableLabel       string
	ChangeRoleLabel    string
	ResetPasswordLabel string
	ForceLogoutLabel   string
	DeleteLabel        string
	ConfirmHint        string
	// TempPassword 是刚重置出来的临时口令，只在这一次响应里展示（M6-2）。
	TempPassword      string
	TempPasswordLabel string
	// PrevHref/NextHref/PrevLabel/NextLabel 是分页链接。
	PrevHref, NextHref   string
	PrevLabel, NextLabel string
	// EmptyLabel 是搜索无结果时的提示。
	EmptyLabel string

	// ---- 注册与邀请页（M6-3）----
	// RegistrationPage 为 true 时模板渲染注册策略、白名单与邀请表。
	RegistrationPage bool
	// 策略区块。
	PolicyHeading         string
	PolicyIntro           string
	PolicyLabel           string
	PolicyOptions         []AdminPolicyOption
	AllowlistLabel        string
	AllowlistHint         string
	AllowlistValue        string
	RegistrationSaveLabel string
	// 邀请列表。
	InvitesHeading   string
	InvitesIntro     string
	InvitesEmpty     string
	ColInviteToken   string
	ColInviteEmail   string
	ColInviteRole    string
	ColInviteStatus  string
	ColInviteCreated string
	ColInviteExpires string
	ColInviteUsedBy  string
	ColInviteActions string
	Invites          []AdminInviteRow
	// 邀请创建表单。
	InviteCreateHeading string
	InviteEmailLabel    string
	InviteRoleLabel     string
	InviteExpiryLabel   string
	InviteExpiryHint    string
	InviteCreateSubmit  string
	InviteRevokeLabel   string
	InviteRoles         []AdminRoleOption
	// 邀请邮件入口（M1-20）。
	// InviteMailAvailable 为 true 时 SMTP 已配置；false 时入口仍渲染，但 note 给出原因。
	InviteMailAvailable bool
	InviteMailLabel     string
	InviteMailNote      string

	// ---- 作业页（M6-6）----
	// JobsPage 为 true 时模板渲染作业表而不是设置区块。
	JobsPage bool
	// Jobs 是当前页的作业行；Page/Pages/Total 复用于分页导航。
	Jobs []AdminJobRow
	// ColJob* 是作业表表头。
	ColJobID, ColJobKind, ColJobStatus, ColJobStage, ColJobCreated, ColJobLog, ColJobActions string
	// JobCancel* 是取消动作的按钮与确认文案。
	JobCancelLabel   string
	JobCancelConfirm string
	// JobLogEmptyLabel 是作业没有日志尾巴时的占位。
	JobLogEmptyLabel string

	// ---- OIDC 配置页（M6-4）----
	// OIDCPage 为 true 时模板渲染 OIDC 配置区块而不是设置表。
	OIDCPage bool
	// OIDC 承载 OIDC 配置页的全部字段；仅 OIDCPage 为 true 时非空。
	OIDC *OIDCPageData
	// ---- SMTP 配置页（M1-17）----
	// SMTPPage 为 true 时模板渲染 SMTP 配置区块而不是设置表。
	SMTPPage bool
	// SMTP 承载 SMTP 配置页的全部字段；仅 SMTPPage 为 true 时非空。
	SMTP *SMTPPageData
	// ---- 审计检索页（M6-7）----
	// AuditPage 为 true 时模板渲染审计检索页。
	AuditPage bool
	// AuditRows 是当前页的审计行。
	AuditRows []AdminAuditRow
	// 过滤表单的标签、当前值与动作下拉。
	AuditFilterHeading string
	AuditUserLabel     string
	AuditUserValue     string
	AuditActionLabel   string
	AuditActions       []AdminOption
	AuditTargetLabel   string
	AuditTargetValue   string
	AuditTargetIDLabel string
	AuditTargetIDValue string
	AuditFromLabel     string
	AuditFromValue     string
	AuditToLabel       string
	AuditToValue       string
	AuditFilterSubmit  string
	AuditFilterClear   string
	AuditRangeHint     string
	AuditClearHref     string
	// ColAudit* 是审计表表头。
	ColAuditTime, ColAuditUser, ColAuditAction, ColAuditTarget, ColAuditDetail string
	// AuditTotalLabel 是「共 N 条」的前缀标签。
	AuditTotalLabel string

	// ---- 健康页（M6-8）----
	// HealthPage 为 true 时模板渲染健康读数表。
	HealthPage bool
	// HealthRows 是健康读数；Key 是稳定英文标识，供测试定位某个值。
	HealthRows []AdminHealthRow

	// ---- API Key 总览（M6-9）----
	// APIKeysPage 为 true 时模板渲染全用户 key 元信息表。
	APIKeysPage bool
	// APIKeyRows 是当前页的 key 行；Page/Pages/Total 复用于分页导航。
	APIKeyRows []AdminAPIKeyRow
	// ColKey* 是 key 表表头。
	ColKeyName, ColKeyOwner, ColKeyPrefix, ColKeyScopes, ColKeyLastUsed, ColKeyExpires, ColKeyState, ColKeyActions string
	// KeyRevokeLabel 是撤销按钮文案。
	KeyRevokeLabel string

	// ---- 语言包完整度报告（M8-4）----
	// I18nPage 为 true 时模板渲染各语言覆盖率表。
	I18nPage bool
	// I18nRows 是每种语言一行。
	I18nRows []AdminI18nRow
	// ColI18n* 是覆盖率表表头。
	ColI18nLocale, ColI18nCoverage, ColI18nStatus, ColI18nMissing string
	// I18nAllComplete 为 true 时页面顶部提示「全部语言包完整」。
	I18nAllComplete bool
	// I18nAllCompleteLabel 是上述提示文案。
	I18nAllCompleteLabel string
}

// AdminOption 是一个下拉/单选项（类型别名兼容 views.SelectOption）。
type AdminOption = SelectOption

// AdminUserRoleOptions 为指定用户的角色下拉列表生成带有正确选中项的配置。
func AdminUserRoleOptions(roles []AdminRoleOption, currentRole string) []SelectOption {
	opts := make([]SelectOption, len(roles))
	for i, r := range roles {
		opts[i] = SelectOption{
			Value:    r.Value,
			Label:    r.Label,
			Selected: r.Value == currentRole,
		}
	}
	return opts
}

// AdminAuditRow 是审计表里的一行（M6-7）。
type AdminAuditRow struct {
	// Time 已按当前管理员时区格式化。
	Time   string
	User   string
	Action string
	Target string
	Detail string
}

// AdminHealthRow 是健康页的一个读数（M6-8）。
type AdminHealthRow struct {
	// Key 是稳定英文标识（database/schema/media/due），只用于测试定位，不展示。
	Key   string
	Label string
	Value string
	Hint  string
}

// AdminAPIKeyRow 是 API Key 总览表里的一行（M6-9）。
//
// 只承载元信息：名称、前缀、scopes、最后使用、过期、状态。绝不携带 key_hash，
// 也不可能有明文——库里根本没有明文列，这里也没有可放它的字段。
type AdminAPIKeyRow struct {
	// ID 是行内 data 属性用的字符串形式主键。
	ID string
	// Owner 是 key 归属者的用户名；查不到时回退 "#<id>"。
	Owner      string
	Name       string
	Prefix     string
	Scopes     string
	LastUsed   string
	Expires    string
	State      string
	StateLabel string
	// CanRevoke 为 true 时该 key 仍可用，模板才渲染撤销入口。
	CanRevoke bool
	// RevokeHref 是撤销表单的提交地址，由 handler 拼好。
	RevokeHref string
}

// AdminI18nRow 是语言包完整度报告里的一行（M8-4）。
type AdminI18nRow struct {
	// Code 是语言码，同时作为测试定位用的 data 属性。
	Code string
	// Percent 是已本地化的覆盖率文本（如 100%）。
	Percent string
	// Counts 是 "实际 / 基准" 的 key 数文本。
	Counts string
	// Complete 为 true 时 StatusLabel 显示「完整」，否则显示「缺失 N 条」。
	Complete    bool
	StatusLabel string
	// MissingKeys 是缺失 key 的逗号分隔列表；完整时为空。
	MissingKeys string
}

// AdminJobRow 是作业表里的一行（M6-6）：状态、阶段、日志尾巴与取消入口。
type AdminJobRow struct {
	ID          uint64
	KindLabel   string
	Status      string
	StatusLabel string
	StageLabel  string
	CreatedAt   string
	StartedAt   string
	FinishedAt  string
	// LogTail 是作业子进程的日志尾巴；模板用 <pre> 等宽展示（templ 会自动转义）。
	LogTail string
	// ErrorText 是失败原因（成功时为空）。
	ErrorText string
	// CanCancel 为 true 时该作业仍可取消（queued 或 running）。
	CanCancel bool
	// CancelHref 是取消表单的提交地址，由 handler 拼好。
	CancelHref string
}

// AdminUserRow 是用户管理表里的一行。
type AdminUserRow struct {
	ID          uint64
	Username    string
	Email       string
	DisplayName string
	Role        string
	RoleLabel   string
	Status      string
	StatusLabel string
	Decks       int64
	Cards       int64
	Reviews     int64
	// IsSelf 标记当前登录管理员自己：模板据此禁用「删除/禁用自己」的入口。
	IsSelf bool
	// 各动作表单的提交地址，由 handler 拼好，模板不拼字符串。
	HrefStatus, HrefRole, HrefPassword, HrefLogout, HrefDelete string
}

// AdminRoleOption 是角色下拉的一项（类型别名兼容 views.SelectOption）。
type AdminRoleOption = SelectOption
