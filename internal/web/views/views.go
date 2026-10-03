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
	// Footer 是页脚链接文本（已本地化）。
	Footer string
	// RepoURL 是页脚链接指向的源码仓库地址。
	RepoURL string
	// LangOptions 是页头语言切换下拉的选项（标签已本地化）。
	LangOptions []LanguageOption
	// LangForm 非 nil 表示当前请求已登录：语言切换以带 CSRF 的 POST 提交（写 users.locale）。
	// 为 nil（访客）时选项渲染为 ?lang= 链接，切完只作用于本次请求（DESIGN.md §8.3）。
	LangForm *LanguageForm
	// LanguageLabel 是语言切换控件的无障碍标签（已本地化）。
	LanguageLabel string
	// CurrentLanguage 是当前语言的显示名（已本地化），用作下拉折叠时的摘要。
	CurrentLanguage string
	// CSSURL / HTMXURL / MathJaxURL 是内容哈希化后的静态资源路径；缺资源时为空串，
	// 模板据此跳过引用（M0-9）。
	CSSURL     string
	HTMXURL    string
	MathJaxURL string
	// IconURL / AppleTouchIconURL 是内容哈希化的图标路径（M8-7）：SVG favicon 与
	// iOS 主屏图标（180×180 PNG）。为空串时模板跳过引用。
	IconURL           string
	AppleTouchIconURL string
	// MediaJSURL 是编辑器媒体上传脚本的内容哈希 URL（M2-9）；为空串时模板跳过引用。
	MediaJSURL string
	// Nav 是顶部导航项（标签已本地化）。
	Nav []NavItem
	// SessionLabel / SessionHref 是页头右侧的会话入口：未登录时指向 /login，已登录时是登出。
	// 文案由 handler 从语言包取好传入，模板不自己判断登录状态。
	SessionLabel string
	SessionHref  string
	// SessionForm 为 true 时用 POST 表单（登出）渲染 SessionLabel，否则渲染普通链接（登录）。
	SessionForm bool
	// CSRF 是登出 POST 表单需要的会话绑定 CSRF token（DESIGN.md §4.3）。
	CSRF string
	// ThemeBootstrap 是内联在 <head>、早于样式表执行的主题引导脚本（M8-8）。它是非空
	// 常量，模板原样输出（templ.Raw），不转义。
	ThemeBootstrap string
}

// NavItem 是一个导航链接。Active 为 true 时它是当前页对应的项（服务端渲染高亮，
// 不再由前端按 href 猜测；M8-7）。
type NavItem struct {
	Label  string
	Href   string
	Active bool
}

// LanguageForm 是页头语言切换下拉的表单参数。切换语言要落进 users.locale，
// 因此它是一个带 CSRF 的 POST（DESIGN.md §4.3、§8.3）。
type LanguageForm struct {
	// Action 是表单提交地址（/settings/locale）。
	Action string
	// CSRF 是会话绑定的 CSRF token。
	CSRF string
	// Next 是写库后回跳的站内地址（已去掉 lang 参数）。
	Next string
	// OOBLocaleControl 为 true 时，本次提交还要带外刷新同页面的语言控件
	// （个人设置页）。否则设置页表单里残留的还是旧语言，用户再点保存会把语言改回去。
	OOBLocaleControl bool
}

// SettingsLocaleControlID 是设置页语言控件的元素 id。它既是带外交换的目标，
// 也是提交时标记"本页有该控件"的值（模板与 handler 共用同一个常量，避免写两遍字符串）。
const SettingsLocaleControlID = "settings-locale-control"

// LanguageSwitchData 是页头语言切换的 htmx 响应片段（就地刷新，不重新加载整页）。
//
// 主交换是 Layout 里的页头下拉；IncludeLocaleControl 为 true 时再带外交换
// 设置页的语言控件，让两处同时反映新语言。
type LanguageSwitchData struct {
	Layout               LayoutData
	IncludeLocaleControl bool
	LocaleLabel          string
	LocaleOptions        []SettingOption
}

// LanguageOption 是页头语言切换下拉里的一枚入口。
//
// 已登录用户走 POST 表单（切换要写 users.locale），此时 Code 是提交的语言码；
// 未登录访客走普通链接，此时 Href 覆盖当前 URL 的 lang 参数。两者互斥。
type LanguageOption struct {
	Label  string
	Href   string
	Code   string
	Active bool
}

// HomeData 是示例首页的渲染数据。
type HomeData struct {
	Layout     LayoutData
	Heading    string
	StartLabel string
	// DecksLabel / DecksHref 是首页进入卡组列表的入口（M2-11）。
	DecksLabel string
	DecksHref  string
}
