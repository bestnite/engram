package web

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"example.com/flashcard/internal/auth"
	"example.com/flashcard/internal/i18n"
	"example.com/flashcard/internal/store"
	"example.com/flashcard/internal/web/views"
)

// 管理面板外壳（DESIGN.md §8.4；AGENTS.md §5 M6-1）。
//
// 硬约束：只有 role = admin 能进；其余一律 403。导航立起全部子页入口，
// 尚未注册路由的子页置灰，避免点进去 404 跳出面板。

const (
	// settingKeySiteName 是站点名称；缺省回退语言包 app.name（DESIGN.md §8.5）。
	settingKeySiteName = "site.name"
	// settingKeySiteDefaultLocale 是站点默认语言，用于语言解析的最后一级回退。
	settingKeySiteDefaultLocale = "site.default_locale"
)

// adminRoute 描述一条已实现的管理路由，注册与「非 admin 全量 403」测试共用同一份清单，
// 避免两处各写一份而漂移。
type adminRoute struct {
	Method string
	Path   string
	// Write 为 true 时该路由是写操作，注册时要挂 CSRF 中间件。
	Write bool
}

// adminRoutes 是当前已注册的 /admin/* 路由全集。新增子页时在这里加一行，
// 测试会据此逐条验证非 admin 访问被拒。
func adminRoutes() []adminRoute {
	return []adminRoute{
		{Method: http.MethodGet, Path: "/admin"},
		{Method: http.MethodGet, Path: "/admin/users"},
		{Method: http.MethodPost, Path: "/admin/users", Write: true},
		{Method: http.MethodPost, Path: "/admin/users/:id/status", Write: true},
		{Method: http.MethodPost, Path: "/admin/users/:id/role", Write: true},
		{Method: http.MethodPost, Path: "/admin/users/:id/password", Write: true},
		{Method: http.MethodPost, Path: "/admin/users/:id/logout", Write: true},
		{Method: http.MethodPost, Path: "/admin/users/:id/delete", Write: true},
		{Method: http.MethodGet, Path: "/admin/registration"},
		{Method: http.MethodPost, Path: "/admin/registration", Write: true},
		{Method: http.MethodPost, Path: "/admin/invites", Write: true},
		{Method: http.MethodPost, Path: "/admin/invites/:id/revoke", Write: true},
		{Method: http.MethodGet, Path: "/admin/settings"},
		{Method: http.MethodPost, Path: "/admin/settings", Write: true},
		{Method: http.MethodGet, Path: "/admin/export"},
	}
}

// registerAdminRoutes 挂载管理面板。会话未装配时跳过，保持 M0 阶段测试可构造。
func (s *Server) registerAdminRoutes(router *gin.Engine) {
	if s.sessions == nil {
		return
	}
	for _, r := range adminRoutes() {
		handlers := []gin.HandlerFunc{s.requireAdmin()}
		if r.Write {
			handlers = append(handlers, s.sessions.CSRFMiddleware())
		}
		switch r.Path {
		case "/admin":
			handlers = append(handlers, s.adminDashboard)
		case "/admin/users":
			if r.Write {
				handlers = append(handlers, s.adminUserCreate)
			} else {
				handlers = append(handlers, s.adminUsersPage)
			}
		case "/admin/users/:id/status":
			handlers = append(handlers, s.adminUserStatus)
		case "/admin/users/:id/role":
			handlers = append(handlers, s.adminUserRole)
		case "/admin/users/:id/password":
			handlers = append(handlers, s.adminUserResetPassword)
		case "/admin/users/:id/logout":
			handlers = append(handlers, s.adminUserForceLogout)
		case "/admin/users/:id/delete":
			handlers = append(handlers, s.adminUserDelete)
		case "/admin/registration":
			if r.Write {
				handlers = append(handlers, s.adminRegistrationSave)
			} else {
				handlers = append(handlers, s.adminRegistrationPage)
			}
		case "/admin/invites":
			handlers = append(handlers, s.adminInviteCreate)
		case "/admin/invites/:id/revoke":
			handlers = append(handlers, s.adminInviteRevoke)
		case "/admin/settings":
			if r.Write {
				handlers = append(handlers, s.adminSettingsSave)
			} else {
				handlers = append(handlers, s.adminSettingsPage)
			}
		case "/admin/export":
			handlers = append(handlers, s.adminExport)
		default:
			continue
		}
		router.Handle(r.Method, r.Path, handlers...)
	}
}

// requireAdmin 是管理面板的唯一入口守卫：未登录回登录页，非 admin 一律 403。
func (s *Server) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		u, ok := auth.CurrentUser(c)
		if !ok {
			// 未登录：没有会话也就没有 CSRF 来源，回到登录页而不是暴露 403 语义。
			c.Redirect(http.StatusSeeOther, "/login")
			c.Abort()
			return
		}
		if u.Role != store.RoleAdmin {
			// 越权尝试留痕：多人共用后这是排查「谁在试」的关键线索。
			s.audit(c.Request.Context(), store.AuditEntry{
				UserID:     store.Ptr(u.ID),
				Action:     store.ActionPermissionDenied,
				TargetType: "admin",
				Detail:     map[string]any{"path": c.Request.URL.Path, "method": c.Request.Method},
			})
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}

// adminNav 构造导航。Implemented=false 的子页渲染成置灰文本：
// 它们尚未注册路由，做成链接会 404 跳出面板（M6-1 要求导航立住全貌）。
func (s *Server) adminNav(loc *i18n.Localizer, active string) []views.AdminNavItem {
	defs := []struct {
		key  string
		href string
		impl bool
	}{
		{"dashboard", "/admin", true},
		{"users", "/admin/users", true},
		{"registration", "/admin/registration", true},
		{"oidc", "/admin/oidc", false},
		{"settings", "/admin/settings", true},
		{"jobs", "/admin/jobs", false},
		{"audit", "/admin/audit", false},
		{"health", "/admin/health", false},
		{"api_keys", "/admin/api-keys", false},
	}
	pending := loc.T("admin.nav.pending")
	out := make([]views.AdminNavItem, 0, len(defs))
	for _, d := range defs {
		out = append(out, views.AdminNavItem{
			Label:        loc.T("admin.nav." + d.key),
			Href:         d.href,
			Active:       d.href == active,
			Implemented:  d.impl,
			PendingLabel: pending,
		})
	}
	return out
}

// adminLayout 构造管理页外壳：品牌名用站点名称的生效值，登出入口复用通用外壳。
func (s *Server) adminLayout(c *gin.Context, loc *i18n.Localizer, titleKey, active string) views.LayoutData {
	layout := views.LayoutData{
		Lang:       loc.Locale(),
		Title:      loc.T(titleKey),
		Brand:      s.siteName(c.Request.Context(), loc),
		HomeURL:    "/",
		Footer:     loc.T("footer.powered_by"),
		CSSURL:     s.assets.URL("css/tailwind.css"),
		HTMXURL:    s.assets.URL("js/htmx.min.js"),
		MathJaxURL: s.assets.URL("js/mathjax/tex-svg.js"),
	}
	layout.SessionLabel = loc.T("nav.logout")
	layout.SessionHref = "/logout"
	layout.SessionForm = true
	if sess, ok := auth.CurrentSession(c); ok {
		layout.CSRF = sess.CSRFToken
	}
	return layout
}

// siteName 返回站点名称的生效值：settings 表 > 语言包 app.name（DESIGN.md §8.5）。
func (s *Server) siteName(ctx context.Context, loc *i18n.Localizer) string {
	if s.db != nil {
		if settings, err := store.LoadSettings(ctx, s.db); err == nil {
			if raw := strings.TrimSpace(settings[settingKeySiteName]); raw != "" {
				return raw
			}
		}
	}
	return loc.T("app.name")
}

// siteDefaultLocale 返回管理员配置的站点默认语言；未配置或不受支持时返回空串。
// 它在语言解析中只是最后一级回退，绝不覆盖 ?lang / 用户设置 / Accept-Language。
func (s *Server) siteDefaultLocale(ctx context.Context) string {
	if s.db == nil {
		return ""
	}
	settings, err := store.LoadSettings(ctx, s.db)
	if err != nil {
		return ""
	}
	raw := strings.TrimSpace(settings[settingKeySiteDefaultLocale])
	for _, code := range s.i18n.SupportedCodes() {
		if code == raw {
			return raw
		}
	}
	return ""
}

// adminDashboard 渲染 /admin 首页：导航全貌 + 一句引导。
func (s *Server) adminDashboard(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	renderHTML(c, views.AdminPage(views.AdminPageData{
		Layout:     s.adminLayout(c, loc, "admin.title", "/admin"),
		Heading:    loc.T("admin.dashboard.heading"),
		Intro:      loc.T("admin.dashboard.intro"),
		NavHeading: loc.T("admin.nav.heading"),
		Nav:        s.adminNav(loc, "/admin"),
	}))
}
