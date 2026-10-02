package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
)

// registerNotFoundRoute 挂上未知路径回退（NoRoute）。
// 必须在其余路由注册之后调用：NoRoute 只在没有任何路由匹配时才触发，注册顺序不改变
// 已注册路由的优先级，但把回退放在最后能避免读代码时误以为它会遮蔽其它路由。
func (s *Server) registerNotFoundRoute(router *gin.Engine) {
	router.NoRoute(s.notFound)
}

// notFound 是未命中任何路由的 catchall 回退（gin NoRoute 语义保证它不会遮蔽已注册路由）。
//
// 分三种出口，避免形态漂移：
//   - /api 子路径：回 internal/api 的统一 JSON 错误包壳，脚本客户端永远拿到 JSON；
//   - /mcp 子路径与非 GET 请求：只回朴素的 404 状态，不塞 HTML；
//   - 其余 GET（真正的页面路径）：渲染设计过的本地化 404 页面。
func (s *Server) notFound(c *gin.Context) {
	path := c.Request.URL.Path
	switch {
	case isAPIPath(path):
		s.apiNotFound(c)
	case c.Request.Method != http.MethodGet || isMCPPath(path):
		c.AbortWithStatus(http.StatusNotFound)
	default:
		s.renderNotFoundPage(c)
	}
}

// apiNotFound 给未知的 /api 子路径回统一错误包壳，字段形态与 internal/api 的 abortError
// 一致（{"error":{"code","message"}}，DESIGN.md §7）；message 走同一份本地化出口。
func (s *Server) apiNotFound(c *gin.Context) {
	code := api.CodeNotFound
	c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": gin.H{
		"code":    code,
		"message": api.ErrorMessage(c.Request.Context(), code),
	}})
}

// renderNotFoundPage 渲染设计过的本地化 404 页面（仅 GET 页面路径）。
func (s *Server) renderNotFoundPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	data := views.NotFoundData{
		Layout: views.LayoutData{
			Lang:       loc.Locale(),
			Title:      loc.T("notfound.title"),
			Brand:      s.siteName(c.Request.Context(), loc),
			HomeURL:    "/",
			Footer:     loc.T("footer.powered_by"),
			CSSURL:     s.assets.URL("css/tailwind.css"),
			HTMXURL:    s.assets.URL("js/htmx.min.js"),
			MathJaxURL: s.assets.URL("js/mathjax/tex-svg.js"),
			Nav: []views.NavItem{
				{Label: loc.T("nav.today"), Href: "/"},
				{Label: loc.T("nav.decks"), Href: "/decks"},
				{Label: loc.T("nav.stats"), Href: "/stats"},
			},
		},
		Code:      strconv.Itoa(http.StatusNotFound),
		Heading:   loc.T("notfound.heading"),
		Message:   loc.T("error.not_found"),
		HomeLabel: loc.T("notfound.home_action"),
		HomeHref:  "/",
	}
	// 页头会话入口沿用通用外壳的规则：已登录显示登出（POST + CSRF），否则显示登录链接。
	if u, ok := auth.CurrentUser(c); ok {
		if u.Role == store.RoleAdmin {
			data.Layout.Nav = append(data.Layout.Nav, views.NavItem{Label: loc.T("nav.admin"), Href: "/admin"})
		}
		data.Layout.Nav = append(data.Layout.Nav, views.NavItem{Label: loc.T("nav.presets"), Href: "/presets"})
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
	// 不能复用 renderHTML：它写死 200。404 必须在写出 body 之前定下状态码。
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(http.StatusNotFound)
	if err := views.NotFound(data).Render(c.Request.Context(), c.Writer); err != nil {
		s.logger.Error("render not found page failed", "error", err, "path", c.Request.URL.Path)
	}
}

// isAPIPath 判断路径是否属于 REST API（/api 与 /api/...）。
func isAPIPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/")
}

// isMCPPath 判断路径是否属于内置 MCP 端点。
func isMCPPath(path string) bool {
	return path == "/mcp" || strings.HasPrefix(path, "/mcp/")
}
