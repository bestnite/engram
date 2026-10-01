package views

// AuthFormData 是登录、注册、首次管理员引导三张表单共用的渲染数据。
// 所有标签文案由 handler 从语言包取好传入；模板不硬编码任何用户可见字符串（AGENTS.md §2.1）。
type AuthFormData struct {
	Layout LayoutData
	// Heading 是页面主标题（已本地化）。
	Heading string
	// Intro 是一段说明文字；为空时不渲染。
	Intro string
	// Action 是表单提交地址，例如 /login、/register、/setup。
	Action string
	// SubmitLabel 是提交按钮文案（已本地化）。
	SubmitLabel string
	// ErrorMessage 是表单校验/认证失败的提示（已本地化）；为空时不渲染。
	ErrorMessage string
	// 各字段标签（已本地化）。
	UsernameLabel    string
	EmailLabel       string
	PasswordLabel    string
	DisplayNameLabel string
	// ShowEmail / ShowDisplayName 控制注册与引导表单展示哪些字段；登录表单只展示用户名与密码。
	ShowEmail       bool
	ShowDisplayName bool
	// EmailValue 是邮箱输入框的预填值；首次管理员引导用 BOOTSTRAP_ADMIN_EMAIL 兜底（DESIGN.md §4.1）。
	EmailValue string
	// PasswordAutocomplete 区分登录（current-password）与注册（new-password）以帮助密码管理器。
	PasswordAutocomplete string
	// AltLabel / AltHref 是表单下方的切换入口（登录↔注册）；AltHref 为空时不渲染。
	AltLabel string
	AltHref  string
	// CSRF 是会话绑定的 CSRF token；为空时不渲染隐藏字段。
	CSRF string
	// InviteToken 是注册表单携带的邀请 token（M1-7）；为空时不渲染隐藏字段。
	InviteToken string
	// LangOptions 是语言切换入口。
	LangOptions []LanguageOption
	// OIDCEnabled 为 true 时登录表单下方渲染可选的 OIDC 登录入口（DESIGN.md §4.4）。
	OIDCEnabled bool
	// OIDCLabel / OIDCHref 是 OIDC 入口的文案与跳转地址；OIDCEnabled 为 false 时忽略。
	OIDCLabel string
	OIDCHref  string
}
