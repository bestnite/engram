package views

// SMTP 配置页（M1-17）的渲染数据。所有文案由 handler 从语言包取好传入，模板不含硬编码文字。

// SMTPPageData 是 SMTP 配置页的全部字段：传输参数、口令配置状态、测试连接与 outbox 读数。
type SMTPPageData struct {
	// FormAction / TestAction 是保存与测试连接的提交地址。
	FormAction string
	TestAction string
	CSRF       string

	HostLabel  string
	HostHint   string
	HostValue  string
	HostSource string

	PortLabel  string
	PortHint   string
	PortValue  string
	PortSource string

	UsernameLabel  string
	UsernameHint   string
	UsernameValue  string
	UsernameSource string

	// PasswordStatus 只显示「已配置/未配置」，绝不回显明文。
	PasswordLabel  string
	PasswordHint   string
	PasswordStatus string
	PasswordSource string

	FromLabel  string
	FromHint   string
	FromValue  string
	FromSource string

	TLSModeLabel string
	TLSModeHint  string
	TLSModeValue string
	// TLSModeOptions 是 none / starttls / implicit 三个选项。
	TLSModeOptions []AdminOption

	SaveLabel string
	TestLabel string
	// TestResult 是测试连接的结论：成功为本地化文案，失败为本地化前缀 + 服务端原始错误文本。
	TestResult string
	TestOK     bool

	// OutboxHeading/Intro 是 outbox 读数区块的标题与说明。
	OutboxHeading string
	OutboxIntro   string
	// OutboxConfiguredLabel 是状态行的标签，OutboxConfiguredStatus 是「已配置/未配置」文案。
	OutboxConfiguredLabel  string
	OutboxConfiguredStatus string
	OutboxPendingLabel     string
	OutboxPending          string
	OutboxFailedLabel      string
	OutboxFailed           string
	OutboxLastErrorLabel   string
	OutboxLastError        string
	OutboxAttemptsLabel    string
	OutboxAttempts         string
}
