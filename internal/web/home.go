package web

import (
	"log/slog"
	"net/http"

	"github.com/a-h/templ"
	"github.com/gin-gonic/gin"

	"example.com/flashcard/internal/auth"
	"example.com/flashcard/internal/i18n"
	"example.com/flashcard/internal/web/views"
)

// mathFormula 演示自托管 MathJax 的 TeX 定界符（DESIGN.md §8.2 用 \( \) 与 \[ \]）。
const mathFormula = `\(a^2 + b^2 = c^2\)`

// home 渲染示例首页：i18n 文案 + 哈希化静态资源引用，二者都从请求上下文/资源清单取。
func (s *Server) home(c *gin.Context) {
	loc := i18n.FromContext(c.Request.Context())
	if loc == nil {
		s.logger.Error("i18n: localizer missing from request context", "path", c.Request.URL.Path)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	data := views.HomeData{
		Layout: views.LayoutData{
			Lang:       loc.Locale(),
			Title:      loc.T("app.name"),
			Brand:      loc.T("app.name"),
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
		Heading:        loc.T("home.heading"),
		Intro:          loc.T("home.intro"),
		StartLabel:     loc.T("home.start_review"),
		LanguagesLabel: loc.T("home.language_label"),
		Formula:        mathFormula,
		Languages:      s.languageOptions(loc),
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
	renderHTML(c, views.Home(data))
}

// languageOptions 生成语言切换入口；语言名同样取自语言包，避免硬编码用户可见文案。
func (s *Server) languageOptions(loc *i18n.Localizer) []views.LanguageOption {
	codes := s.i18n.SupportedCodes()
	out := make([]views.LanguageOption, 0, len(codes))
	for _, code := range codes {
		out = append(out, views.LanguageOption{
			Label:  loc.T("language." + code),
			Href:   "/?lang=" + code,
			Active: loc.Locale() == code,
		})
	}
	return out
}

// renderHTML 统一写出 HTML 响应。渲染失败时响应头可能已发出，只能记一条英文日志
// （AGENTS.md §2.1：日志恒为英文）。
func renderHTML(c *gin.Context, comp templ.Component) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(http.StatusOK)
	if err := comp.Render(c.Request.Context(), c.Writer); err != nil {
		slog.Error("render template failed", "error", err, "path", c.Request.URL.Path)
	}
}
