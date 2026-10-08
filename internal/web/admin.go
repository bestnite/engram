package web

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/store"
)

// 管理面板入口（ROADMAP.md M6-1）。
//
// 硬约束：只有 role = admin 能进；其余一律 403。SSR 页面层已删除：每条 GET 路由先过
// requireAdmin，再发 SPA 应用壳；面板的读写在 /api/v1/admin/* 的 JSON 端点上（admin_api.go 起的那几个文件）。

const (
	// settingKeySiteName 是站点名称；缺省回退语言包 app.name。
	settingKeySiteName = "site.name"
	// settingKeySiteDefaultLocale 是站点默认语言，用于语言解析的最后一级回退。
	settingKeySiteDefaultLocale = "site.default_locale"
)

// adminRoute 描述一条已实现的管理路由，注册与「非 admin 全量 403」测试共用同一份清单，
// 避免两处各写一份而漂移。
type adminRoute struct {
	Method string
	Path   string
}

// adminRoutes 是当前已注册的 /admin/* 路由全集（SSR 页面层删除后只剩 GET 页面外壳）。
// 新增子页时在这里加一行，测试会据此逐条验证非 admin 访问被拒。
func adminRoutes() []adminRoute {
	return []adminRoute{
		{Method: http.MethodGet, Path: "/admin"},
		{Method: http.MethodGet, Path: "/admin/users"},
		{Method: http.MethodGet, Path: "/admin/registration"},
		{Method: http.MethodGet, Path: "/admin/settings"},
		{Method: http.MethodGet, Path: "/admin/jobs"},
		{Method: http.MethodGet, Path: "/admin/oidc"},
		{Method: http.MethodGet, Path: "/admin/smtp"},
		{Method: http.MethodGet, Path: "/admin/audit"},
		{Method: http.MethodGet, Path: "/admin/health"},
		{Method: http.MethodGet, Path: "/admin/api-keys"},
		{Method: http.MethodGet, Path: "/admin/i18n"},
		{Method: http.MethodGet, Path: "/admin/mail-templates"},
	}
}

// registerAdminRoutes 挂载管理面板的页面外壳与 JSON 端点。
// 会话未装配时跳过，保持 M0 阶段测试可构造。
func (s *Server) registerAdminRoutes(router *gin.Engine) {
	if s.sessions == nil {
		return
	}
	// 每条页面路由都是 requireAdmin + 应用壳：requireAdmin 先跑，非 admin 拿不到外壳。
	for _, r := range adminRoutes() {
		router.Handle(r.Method, r.Path, s.requireAdmin(), s.adminShell)
	}
	// SPA 的 JSON 端点（/api/v1/admin/*），判权在 adminGuard。
	s.registerAdminAPIRoutes(router)
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

// siteName 返回站点名称的生效值：settings 表 > 语言包 app.name。
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
