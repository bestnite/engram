package views

// ShareBrowseNote 是公开只读浏览页里的一条卡片内容（正反面已渲染为清洗后的 HTML）。
// 刻意不含任何进度字段：匿名访问者看到的只有内容，不泄漏任何用户的排程数据。
type ShareBrowseNote struct {
	FrontHTML string
	BackHTML  string
}

// ShareBrowseData 是免注册只读浏览页（M5-3）的全部渲染数据；所有用户可见字符串已本地化。
type ShareBrowseData struct {
	Layout   LayoutData
	Heading  string
	DeckName string
	Intro    string
	// 复习入口只提示登录：进度必须挂在账号上（DESIGN.md §5）。
	ReviewPrompt string
	LoginLabel   string
	LoginHref    string
	FrontLabel   string
	BackLabel    string
	Notes        []ShareBrowseNote
	EmptyText    string
	// 口令门（可选）。
	PasswordRequired    bool
	PasswordLabel       string
	PasswordPlaceholder string
	UnlockSubmit        string
	PasswordError       string
	UnlockAction        string
}
