package views

// OIDC 配置页（M6-4）的渲染数据。所有文案由 handler 从语言包取好传入，模板不含硬编码文字。

// OIDCPageData 是 OIDC 配置页的全部字段：启用开关、参数、claim 映射、测试连接与已绑定身份列表。
type OIDCPageData struct {
	// FormAction / TestAction 是保存与测试连接的提交地址。
	FormAction string
	TestAction string
	CSRF       string

	EnabledLabel string
	EnabledHint  string
	Enabled      bool

	IssuerLabel string
	IssuerHint  string
	IssuerValue string

	ClientIDLabel string
	ClientIDValue string

	SecretLabel  string
	SecretHint   string
	SecretStatus string

	ScopesLabel string
	ScopesHint  string
	ScopesValue string

	ClaimHeading            string
	ClaimSubjectLabel       string
	ClaimSubjectValue       string
	ClaimEmailLabel         string
	ClaimEmailValue         string
	ClaimNameLabel          string
	ClaimNameValue          string
	ClaimEmailVerifiedLabel string
	ClaimEmailVerifiedValue string

	SaveLabel string
	TestLabel string
	// TestResult 是测试连接的结论：成功为本地化文案，失败为本地化前缀 + provider 原始错误文本。
	TestResult string
	TestOK     bool

	IdentitiesHeading string
	IdentitiesIntro   string
	IdentitiesEmpty   string
	ColProvider       string
	ColSubject        string
	ColEmail          string
	ColUser           string
	ColLinked         string
	ColActions        string
	UnlinkLabel       string
	Identities        []OIDCIdentityRow
}

// OIDCIdentityRow 是已绑定身份列表里的一行。
type OIDCIdentityRow struct {
	Provider   string
	Subject    string
	Email      string
	Username   string
	LinkedAt   string
	UnlinkHref string
}
