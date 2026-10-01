// Package views 存放 templ 模板与它们的渲染数据。
//
// 模板不自己读取全局状态：用户可见文案由 handler 从请求上下文里的 translator 取好，
// 静态资源 URL 由 handler 从资源清单取好，统一作为数据传入。这样模板可以脱离 HTTP
// 栈单独渲染与测试，也不需要在模板内部依赖 gin 或 embed（AGENTS.md §2.4）。
// 生成模板的 Go 代码：在仓库根执行 `go generate ./...` 即可（templ CLI 需在 PATH）。
//
//go:generate templ generate
package views

// LayoutData 是页面外壳（base.templ）的渲染数据。
type LayoutData struct {
	// Lang 是 <html lang> 的取值，形如 zh-CN 或 en。
	Lang string
	// Title 是 <title> 文本（已本地化）。
	Title string
	// Brand 是页头左侧的品牌名（已本地化）。
	Brand string
	// HomeURL 是品牌链接指向的地址。
	HomeURL string
	// Footer 是页脚文本（已本地化）。
	Footer string
	// CSSURL / HTMXURL / MathJaxURL 是内容哈希化后的静态资源路径；缺资源时为空串，
	// 模板据此跳过引用（M0-9）。
	CSSURL     string
	HTMXURL    string
	MathJaxURL string
	// Nav 是顶部导航项（标签已本地化）。
	Nav []NavItem
}

// NavItem 是一个导航链接。
type NavItem struct {
	Label string
	Href  string
}

// LanguageOption 是示例页上的一枚语言切换入口。
type LanguageOption struct {
	Label  string
	Href   string
	Active bool
}

// HomeData 是示例首页的渲染数据。
type HomeData struct {
	Layout         LayoutData
	Heading        string
	Intro          string
	StartLabel     string
	LanguagesLabel string
	// Formula 是演示 MathJax 自托管渲染的数学表达式，不是用户可见自然语言文案。
	Formula   string
	Languages []LanguageOption
}
