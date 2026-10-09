package web

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是管理面板的公共装配：
//   - adminShell 发管理面板 GET 页面的应用壳（requireAdmin 已在它之前跑过）；
//   - adminGuard 给 /api/v1/admin/* 的 JSON 端点做与 requireAdmin 等价的判权，
//     但以 JSON 401/403 回应（浏览器 fetch 不能跟着 303 去登录页拿 HTML）；
//   - registerAdminAPIRoutes 挂载全部 JSON 端点。
//
// 判权语义与页面守卫 requireAdmin 逐条对齐：未登录 401、非 admin 403 并写
// permission.denied 审计、bearer/API Key 不属于浏览器会话一律 403。授权判定只在服务端，
// 前端拿不到任何未授权数据。

// adminShell 是管理面板每条 GET 页面路由的处理器：requireAdmin 已在它之前跑过
// （未登录 303 到登录页、非 admin 403），这里只发 SPA 应用壳。
// SSR 页面层已删除，不再回退任何 SSR 页面。
func (s *Server) adminShell(c *gin.Context) {
	s.shell.ServeIndex(c)
}

// adminError 写出管理 JSON 端点的错误包壳：code 稳定且英文，message 为英文兜底文案，
// 前端按 code 映射本地化提示。
func adminError(c *gin.Context, status int, code string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{
		"code":    code,
		"message": api.ErrorMessage(c.Request.Context(), code),
	}})
}

// adminGuard 是 /api/v1/admin/* 的唯一入口守卫，与 requireAdmin 同源同判：
// 未登录（或已禁用）401、非 admin 403（并写越权审计）、bearer/API Key 403。
func (s *Server) adminGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		// bearer 与 API Key 凭据不属于浏览器会话，管理端点只接受会话 cookie。
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.GetHeader("Authorization"))), "bearer ") {
			adminError(c, http.StatusForbidden, api.CodeForbidden)
			c.Abort()
			return
		}
		if _, ok := api.CurrentAPIKey(c); ok {
			adminError(c, http.StatusForbidden, api.CodeForbidden)
			c.Abort()
			return
		}
		u, ok := auth.CurrentUser(c)
		if !ok || u.Status != store.StatusActive {
			adminError(c, http.StatusUnauthorized, api.CodeUnauthorized)
			c.Abort()
			return
		}
		if u.Role != store.RoleAdmin {
			// 越权尝试留痕：与 requireAdmin 完全同一份审计，便于排查「谁在试」。
			s.audit(c.Request.Context(), store.AuditEntry{
				UserID:     store.Ptr(u.ID),
				Action:     store.ActionPermissionDenied,
				TargetType: "admin",
				Detail:     map[string]any{"path": c.Request.URL.Path, "method": c.Request.Method},
			})
			adminError(c, http.StatusForbidden, api.CodeForbidden)
			c.Abort()
			return
		}
		c.Next()
	}
}

// registerAdminAPIRoutes 挂载管理面板的 JSON 端点（读 + 写）。
//
// 依赖未装配时整体跳过，保证 M0 阶段与未启用会话的测试仍能构造 Server。写操作一律过会话
// CSRF。管理面板的读写只有这一批 JSON 端点（SSR 表单端点已删除）。
func (s *Server) registerAdminAPIRoutes(router *gin.Engine) {
	if s.sessions == nil || s.users == nil {
		return
	}
	g := router.Group("/api/v1/admin")
	g.Use(s.adminGuard())

	// 只读端点（概览 / 健康 / 审计）。
	g.GET("/summary", s.adminSummary)
	g.GET("/health", s.adminHealth)
	g.GET("/audit", s.adminAudit)

	// 用户管理（列表读 + 危险动作写；写操作过会话 CSRF）。
	g.GET("/users", s.adminUsers)
	g.POST("/users", s.sessions.CSRFMiddleware(), s.adminUserCreate)
	g.POST("/users/:id/status", s.sessions.CSRFMiddleware(), s.adminUserStatus)
	g.POST("/users/:id/role", s.sessions.CSRFMiddleware(), s.adminUserRole)
	g.POST("/users/:id/password", s.sessions.CSRFMiddleware(), s.adminUserResetPassword)
	g.POST("/users/:id/logout", s.sessions.CSRFMiddleware(), s.adminUserForceLogout)
	g.POST("/users/:id/delete", s.sessions.CSRFMiddleware(), s.adminUserDelete)

	// 邮件模板：列表读 + 保存 / 删除 / 预览 / 测试发信（写操作过 CSRF）。
	g.GET("/mail-templates", s.adminMailTemplates)
	g.PUT("/mail-templates/:type/:locale", s.sessions.CSRFMiddleware(), s.adminMailTemplateSave)
	g.DELETE("/mail-templates/:type/:locale", s.sessions.CSRFMiddleware(), s.adminMailTemplateDelete)
	g.POST("/mail-templates/preview", s.sessions.CSRFMiddleware(), s.adminMailTemplatePreview)
	g.POST("/mail-templates/test", s.sessions.CSRFMiddleware(), s.adminMailTemplateTest)

	// 注册与邀请。
	g.GET("/registration", s.adminRegistration)
	g.POST("/registration", s.sessions.CSRFMiddleware(), s.adminRegistrationSave)
	g.POST("/invites", s.sessions.CSRFMiddleware(), s.adminInviteCreate)
	g.POST("/invites/:id/revoke", s.sessions.CSRFMiddleware(), s.adminInviteRevoke)

	// API Key 总览（读 + 撤销）。
	g.GET("/api-keys", s.adminAPIKeys)
	g.POST("/api-keys/:id/revoke", s.sessions.CSRFMiddleware(), s.adminAPIKeyRevoke)

	// 系统设置（读 + 写；敏感键经加密落库）。
	g.GET("/settings", s.adminSettings)
	g.POST("/settings", s.sessions.CSRFMiddleware(), s.adminSettingsSave)

	// 邮件（SMTP）配置与测试连接。
	g.GET("/smtp", s.adminSMTP)
	g.POST("/smtp", s.sessions.CSRFMiddleware(), s.adminSMTPSave)
	g.POST("/smtp/test", s.sessions.CSRFMiddleware(), s.adminSMTPTest)

	// 身份与 OIDC 配置、测试连接与解绑。
	g.GET("/oidc", s.adminOIDC)
	g.POST("/oidc", s.sessions.CSRFMiddleware(), s.adminOIDCSave)
	g.POST("/oidc/test", s.sessions.CSRFMiddleware(), s.adminOIDCTest)
	g.POST("/oidc/identities/:id/unlink", s.sessions.CSRFMiddleware(), s.adminOIDCUnlink)

	// 作业列表与取消。
	g.GET("/jobs", s.adminJobs)
	g.POST("/jobs/:id/cancel", s.sessions.CSRFMiddleware(), s.adminJobCancel)

	// 语言包完整度报告（只读）。
	g.GET("/i18n", s.adminI18n)
}
