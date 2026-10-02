package views

// 本文件是 M1-8 个人设置页（/settings）的渲染数据。
// 所有面向用户的文字都由 handler 从语言包取好后放入这些字段，模板只做排版
// （AGENTS.md §2.1；scripts/checks/no-template-literals.sh 会强制这一点）。

// SettingOption 是下拉框里的一个选项（语言、时区候选）。
type SettingOption = SelectOption

// SettingsData 是个人设置页的渲染数据。
type SettingsData struct {
	Layout       LayoutData
	Heading      string
	Intro        string
	ErrorMessage string
	SavedMessage string

	// 资料表单：显示名、语言、时区、复习日切点。
	ProfileHeading   string
	DisplayNameLabel string
	DisplayNameValue string
	LocaleLabel      string
	LocaleOptions    []SettingOption
	TimezoneLabel    string
	TimezoneValue    string
	TimezoneHint     string
	// TimezoneOptions 是 datalist 的常用时区候选；用户仍可手输任意 IANA 名。
	TimezoneOptions []string
	CutoffLabel     string
	CutoffValue     string
	CutoffHint      string
	ProfileSubmit   string

	// 改密码表单。PasswordAvailable 为 false 时（纯 OIDC 账号）不渲染表单。
	PasswordHeading     string
	OldPasswordLabel    string
	NewPasswordLabel    string
	PasswordSubmit      string
	PasswordNote        string
	PasswordAvailable   bool
	PasswordUnavailable string

	// TOTP 二次验证入口（M1-16）：指向 /settings/totp 的独立页面。
	TOTPHeading   string
	TOTPHint      string
	TOTPLinkLabel string
	TOTPHref      string

	// 邮件类型偏好入口（M1-18）：指向 /settings/notifications 的独立页面。
	MailPrefsHeading   string
	MailPrefsHint      string
	MailPrefsLinkLabel string
	MailPrefsHref      string

	// 邮箱地址变更入口（M1-19）：指向 /settings/email 的独立页面。
	// EmailAvailable 为 false 时（SMTP / 安全邮件栈未启用）整张卡片不渲染，
	// 避免把用户送到一个只会显示「邮件未配置」的死路。
	EmailAvailable bool
	EmailHeading   string
	EmailHint      string
	EmailLinkLabel string
	EmailHref      string

	// 用户级 API Key 管理入口（M4-10）：指向 /settings/keys 的独立页面。
	KeysHeading   string
	KeysHint      string
	KeysLinkLabel string
	KeysHref      string

	CSRF string
}
