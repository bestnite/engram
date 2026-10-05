package views

// 本文件是 M4-10「我的 API Key」页（/settings/keys）的渲染数据。
// 所有面向用户的文字都由 handler 从语言包取好后放入这些字段，模板只做排版
// （AGENTS.md §2.1）。

// ScopeOption 是创建表单里的一个 scope 复选框。
type ScopeOption struct {
	Value string
	Label string
}

// APIKeyRow 是列表里的一把 key；只含元信息，绝不含明文或哈希。
type APIKeyRow struct {
	ID         string
	Name       string
	Prefix     string
	Scopes     string
	LastUsed   string
	Expires    string
	StateLabel string
	CanRevoke  bool
	RevokeHref string
}

// KeysData 是「我的 API Key」页的渲染数据。
type KeysData struct {
	Layout       LayoutData
	Heading      string
	Intro        string
	ErrorMessage string

	// 一次性明文横幅：创建成功后显示一次，之后任何 GET 都不再出现。
	NewKeyNotice string
	NewKeyPlain  string

	// 列表区块。
	ColName     string
	ColPrefix   string
	ColScopes   string
	ColLastUsed string
	ColExpires  string
	ColState    string
	ColActions  string
	Rows        []APIKeyRow
	EmptyText   string
	RevokeLabel string

	// 创建表单区块。
	CreateHeading   string
	NameLabel       string
	NamePlaceholder string
	ScopesLabel     string
	ScopesHint      string
	ScopeOptions    []ScopeOption
	ExpiresLabel    string
	ExpiresHint     string
	CreateSubmit    string
	CreateAction    string
	CSRF            string
}
