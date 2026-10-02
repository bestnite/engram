package views

// 本文件是 M1-16（TOTP 二次验证）的渲染数据。
// 所有面向用户的文字都由 handler 从语言包取好后放入这些字段，模板只做排版（AGENTS.md §2.1）。

// TOTPChallengeData 是登录第二步（输入验证码或恢复码）页面的渲染数据。
type TOTPChallengeData struct {
	Layout       LayoutData
	Heading      string
	Intro        string
	CodeLabel    string
	RecoveryHint string
	SubmitLabel  string
	ErrorMessage string
	// CSRF 是双提交 cookie 的镜像 token（登录前流程没有会话绑定的 token）。
	CSRF string
}

// TOTPSettingsData 是个人设置页里的 TOTP 区块（/settings/totp）的渲染数据。
type TOTPSettingsData struct {
	Layout       LayoutData
	Heading      string
	Intro        string
	ErrorMessage string
	SavedMessage string

	// Enabled 为 true 时展示「已启用」状态与关闭表单；否则展示状态与启用入口。
	Enabled             bool
	StatusEnabledLabel  string
	StatusDisabledLabel string

	BeginHint   string
	BeginSubmit string

	// Pending 为 true 时展示待确认绑定的 secret / otpauth 链接与确认表单。
	Pending        bool
	PendingHeading string
	SecretLabel    string
	SecretValue    string
	OtpauthLabel   string
	OtpauthURL     string
	ConfirmHint    string
	ConfirmSubmit  string

	DisableHeading string
	DisableHint    string
	PasswordLabel  string
	DisableSubmit  string

	RecoveryHeading     string
	RecoveryRemaining   string
	RecoveryWarning     string
	RecoveryCodesLabel  string
	RecoveryCodes       []string
	RecoveryRegenSubmit string

	CSRF string
}
