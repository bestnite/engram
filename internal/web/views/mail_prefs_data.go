package views

// 本文件是 M1-18 邮件偏好页（/settings/notifications）的渲染数据。
// 所有面向用户的文字都由 handler 从语言包取好后放入这些字段，模板只做排版
// （AGENTS.md §2.1）。

// MailPrefsData 是邮件偏好页的渲染数据。
type MailPrefsData struct {
	Layout       LayoutData
	Heading      string
	Intro        string
	ErrorMessage string
	SavedMessage string
	SubmitLabel  string
	// Groups 是四个大类（A/B/C/D）及其下的类型，顺序由目录固定。
	Groups []MailPrefGroup
	CSRF   string
}

// MailPrefGroup 是一个大类的渲染分组。
type MailPrefGroup struct {
	Heading string
	// Types 是归入该类、在偏好页展示的类型。
	Types []MailPrefType
}

// MailPrefType 是偏好页上的一个类型开关。
type MailPrefType struct {
	Label string
	// InputName 是复选框的 name（mail_pref.<type>）；Locked 为 true 时模板不渲染它。
	InputName string
	// Enabled 是当前有效开关（用户显式选择优先，否则取目录默认）。
	Enabled bool
	// Locked 为 true 表示该类不可关闭：模板渲染成禁用复选框，并显示 LockedReason。
	Locked bool
	// LockedReason 是「为什么不能关」的本地化文案，只在 Locked 时使用。
	LockedReason string
}
