package views

// SecurityFormData 是密码重置、邮箱验证与改邮箱确认这些「安全/事务」页面的渲染数据。
// 与 AuthFormData 分开：AuthPage 恒定渲染用户名输入，而这些流程不需要用户名，硬塞进去
// 会在页面上多出一个无意义字段。所有文案由 handler 从语言包取好传入，模板零硬编码。
type SecurityFormData struct {
	Layout LayoutData
	// Heading 是页面主标题（已本地化）。
	Heading string
	// Intro 是一段说明文字；为空时不渲染。
	Intro string
	// Notice 是一条中性/成功提示（例如「重置邮件已发送」）；为空时不渲染。
	Notice string
	// ErrorMessage 是失败提示（已本地化）；为空时不渲染。
	ErrorMessage string
	// ShowForm 为 false 时只渲染 Heading/Intro/Notice/ErrorMessage（结果页）。
	ShowForm bool
	// Action 是表单提交地址；ShowForm 为 true 时使用。
	Action string
	// CSRF 是会话或双提交绑定的 token；为空时不渲染隐藏字段。
	CSRF string
	// ShowEmail / EmailLabel / EmailValue 控制邮箱输入。
	ShowEmail  bool
	EmailLabel string
	EmailValue string
	// ShowPassword / PasswordLabel 控制密码输入。
	ShowPassword  bool
	PasswordLabel string
	// SubmitLabel 是提交按钮文案。
	SubmitLabel string
	// AltLabel / AltHref 是表单下方的返回入口；AltHref 为空时不渲染。
	AltLabel string
	AltHref  string
}
