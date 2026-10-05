package web

import (
	"net/url"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
)

// repositoryURL 是页脚源码仓库链接。它是公开托管地址，属模块路径例外（AGENTS.md §2.2），
// 已登记在 AGENTS.md §2.2 的模块路径例外里；绝不写进模板，因为模板禁止硬编码可见文案。
const repositoryURL = "https://git.nite07.com/nite/engram"

// languageRoute 是已登录用户提交页头语言切换的端点。切换语言要写 users.locale，
// 因此不能走 GET 的 ?lang=（DESIGN.md §8.3）；它与数据模型 users.locale、表单字段
// 名以及个人设置页的语言取值保持同一个词，便于检索。
const languageRoute = "/settings/locale"

// themeBootstrap 在 <head> 内联执行主题引导（M8-8）。它必须内联且早于样式表：外链的
// pwa.js 是独立网络请求，浏览器可能在它执行前就先画出白底一帧；暗色下这就是可见的白闪。
// 这里只做「首帧之前必须成立」的最小集合——暗色类、color-scheme、画布底色与 theme-color，
// 用内联 style 设底色，任何样式表都抢不到这个竞态。颜色值与 pwa.js 的 applyTheme 保持一致。
// 常量自带 <script> 标签、由模板 @rawHTML 原样输出：templ 把 <script> 当纯文本元素，
// 写在其内部的 @rawHTML(...) 会被当成字面文本；模板里也不放内联 JS，所以这段引导脚本作为
// 常量经 @rawHTML 输出（decks.templ 的 noscriptDialogStyle 同理）。
const themeBootstrap = `<script>(function(){var s;try{s=localStorage.getItem("engram-theme")}catch(e){}var d=s==="dark"||(s!=="light"&&window.matchMedia&&window.matchMedia("(prefers-color-scheme: dark)").matches);var r=document.documentElement;r.classList.toggle("dark",d);r.style.colorScheme=d?"dark":"light";r.style.backgroundColor=d?"#09090b":"#f8fafc";var m=document.querySelector('meta[name="theme-color"]');if(m){m.setAttribute("content",d?"#09090b":"#ffffff")}})();</script>`

// decorateLayout 补齐所有页面外壳共有的字段：页脚项目名与仓库链接、页头语言切换下拉、
// 以及哈希化图标路径（M8-7）。每个 LayoutData 构造完后调用一次，避免多个外壳各自重复。
func (s *Server) decorateLayout(c *gin.Context, loc *i18n.Localizer, layout *views.LayoutData) {
	// 主题引导对每个页面都一样，集中在这里赋值，避免五处 LayoutData 构造点各写一遍。
	layout.ThemeBootstrap = themeBootstrap
	// 页脚固定显示项目名 Engram（不取站点名覆盖）：页脚表达的是项目身份而非站点配置（M8-7）。
	layout.Footer = loc.T("app.name")
	layout.RepoURL = repositoryURL
	// 图标走内容哈希路径（DESIGN.md §8.5）；缺资源时为空串，模板跳过引用。
	layout.IconURL = s.assets.URL("icons/icon.svg")
	layout.AppleTouchIconURL = s.assets.URL("icons/apple-touch-icon.png")
	layout.LangOptions = s.languageOptions(c, loc)
	layout.LanguageLabel = loc.T("home.language_label")
	layout.CurrentLanguage = loc.T("language." + loc.Locale())
	// 已登录用户切换语言必须落库：用带 CSRF 的 POST 表单，否则下一次不带 ?lang 的
	// 请求（点任一导航链接或刷新）就会回退。访客没有资料可存，继续用 ?lang= 链接。
	if csrf := sessionCSRF(c); csrf != "" {
		layout.LangForm = &views.LanguageForm{
			Action: languageRoute,
			CSRF:   csrf,
			Next:   langNextURL(c.Request.URL),
		}
	}
}

// mainNav 构造顶部导航项，是全站唯一的导航来源（M8-7）。
//
// 顺序固定：今日 / 卡组 / 统计；管理仅管理员可见；调度预设与设置仅登录用户可见。
// active 是当前页面的路径，命中者标记 Active，由模板服务端渲染高亮——不再由前端脚本
// 按 href 猜测（旧做法会把品牌链接也算进导航，见 M8-7 缺陷 (a)）。
func (s *Server) mainNav(c *gin.Context, loc *i18n.Localizer, active string) []views.NavItem {
	items := []views.NavItem{
		{Label: loc.T("nav.today"), Href: "/"},
		{Label: loc.T("nav.decks"), Href: "/decks"},
		{Label: loc.T("nav.stats"), Href: "/stats"},
	}
	if u, ok := auth.CurrentUser(c); ok {
		// 管理入口只给管理员，避免普通用户点进去吃 403（M6-1）。
		if u.Role == store.RoleAdmin {
			items = append(items, views.NavItem{Label: loc.T("nav.admin"), Href: "/admin"})
		}
		// 预设与个人设置对每个已登录用户可见（M9-9）。
		items = append(items,
			views.NavItem{Label: loc.T("nav.presets"), Href: "/presets"},
			views.NavItem{Label: loc.T("nav.settings"), Href: "/settings"},
		)
	}
	for i := range items {
		items[i].Active = items[i].Href == active
	}
	return items
}

// languageOptions 生成页头语言切换的入口：已登录用户带提交用的语言码（由布局里的
// LangForm 决定渲染成 POST 表单），访客带指向当前页面的链接（覆盖 lang 参数）。
// 链接不直接拼接原始 query，否则在 /decks?page=2 这类页面上切换语言会丢掉参数。
func (s *Server) languageOptions(c *gin.Context, loc *i18n.Localizer) []views.LanguageOption {
	codes := s.i18n.SupportedCodes()
	loggedIn := sessionCSRF(c) != ""
	out := make([]views.LanguageOption, 0, len(codes))
	for _, code := range codes {
		opt := views.LanguageOption{
			Label:  loc.T("language." + code),
			Active: loc.Locale() == code,
		}
		if loggedIn {
			opt.Code = code
		} else {
			opt.Href = localeURL(c.Request.URL, code)
		}
		out = append(out, opt)
	}
	return out
}

// localeURL 复制 u 的路径与查询参数并覆盖 lang 参数；用 url.Values 编码，避免重复或注入。
func localeURL(u *url.URL, code string) string {
	path := u.Path
	if path == "" {
		path = "/"
	}
	q := u.Query()
	q.Set("lang", code)
	return path + "?" + q.Encode()
}

// langNextURL 生成语言切换落库后回跳的地址：保留当前路径与查询参数，但去掉 lang 参数。
// 切换已写进 users.locale，语言由存储值决定；留着旧的 lang 会继续覆盖它，
// 用户会觉得"切了没生效"——那正是本次要修的缺陷。
func langNextURL(u *url.URL) string {
	path := u.Path
	if path == "" {
		path = "/"
	}
	q := u.Query()
	q.Del("lang")
	if enc := q.Encode(); enc != "" {
		return path + "?" + enc
	}
	return path
}
