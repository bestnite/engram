package web

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/web/views"
)

// homeRoute 提供 GET /（首页「今日」，DESIGN.md §8.1）。三条分支互斥，书写顺序即优先级：
//
//  1. 首启窗口（M1-25）：还没有活跃管理员时首页没有任何可展示的内容，把访客直接送去引导页，
//     连 SPA 外壳都不给。`/setup` 在管理员出现后自身 404（见 setupPage），所以这条重定向只在
//     首启窗口内生效，不会变成常驻跳转，也不会与 SPA 首页形成循环——判定只看管理员数量，
//     与是否登录无关，未登录访客在管理员已存在时不会被再次送回 `/setup`。
//  2. SPA 已加载：返回应用壳，由客户端路由渲染首页，数据走 JSON 端点（DESIGN.md §8.1、§8.5）。
//  3. 降级：SPA 缺失时回退 SSR 首页 homePage，模板与逻辑保持不变。
func (s *Server) homeRoute(c *gin.Context) {
	// 不带 ?lang：语言由引导页按 Accept-Language 与 cookie 自行解析。
	// Users 依赖缺失时不妄断（判定会 panic），退回后续渲染路径。
	if s.users != nil && s.setupAvailable(c) {
		c.Redirect(http.StatusSeeOther, "/setup")
		return
	}
	if s.spa != nil {
		s.spa.ServeIndex(c)
		return
	}
	s.homePage(c)
}

// homePage 渲染 SSR 示例首页：i18n 文案 + 哈希化静态资源引用，二者都从请求上下文/资源清单取。
// 只在 SPA 缺失（降级）时由 homeRoute 调用；首启窗口的重定向已在 homeRoute 内先行处理。
func (s *Server) homePage(c *gin.Context) {
	loc := i18n.FromContext(c.Request.Context())
	if loc == nil {
		s.logger.Error("i18n: localizer missing from request context", "path", c.Request.URL.Path)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	layout := views.LayoutData{
		Lang:       loc.Locale(),
		Title:      loc.T("app.name"),
		Brand:      s.siteName(c.Request.Context(), loc),
		HomeURL:    "/",
		CSSURL:     s.assets.URL("css/tailwind.css"),
		HTMXURL:    s.assets.URL("js/htmx.min.js"),
		MathJaxURL: s.assets.URL("js/mathjax/tex-svg.js"),
	}
	// 语言切换下拉、页脚与哈希化图标对所有页面外壳一致，这里补齐。
	s.decorateLayout(c, loc, &layout)
	// 顶部导航走全站唯一构造器（M8-7）；当前页是首页，高亮「今日」。
	layout.Nav = s.mainNav(c, loc, "/")
	data := views.HomeData{
		Layout:     layout,
		Heading:    loc.T("home.heading"),
		StartLabel: loc.T("home.start_review"),
		DecksLabel: loc.T("home.decks_link"),
		DecksHref:  "/decks",
	}
	// 页头右侧的会话入口由当前登录状态决定：已登录显示登出（POST + CSRF），否则显示登录链接。
	if _, ok := auth.CurrentUser(c); ok {
		data.Layout.SessionLabel = loc.T("nav.logout")
		data.Layout.SessionHref = "/logout"
		data.Layout.SessionForm = true
		if sess, ok := auth.CurrentSession(c); ok {
			data.Layout.CSRF = sess.CSRFToken
		}
	} else {
		data.Layout.SessionLabel = loc.T("nav.login")
		data.Layout.SessionHref = "/login"
	}
	renderHTMLStatus(c, http.StatusOK, views.Home(data))
}
