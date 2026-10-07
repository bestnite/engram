package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是管理面板迁到 SPA（DESIGN.md §8.1、§8.4）的公共装配：
//   - spaAdminPage 把 SSR 页面处理器包成「有 SPA 就发应用壳、没有就回退 SSR」的 GET 处理器；
//   - spaAdminGuard 给 /api/v1/admin/* 的 JSON 端点做与 requireAdmin 等价的判权，
//     但以 JSON 401/403 回应（浏览器 fetch 不能跟着 303 去登录页拿 HTML）；
//   - registerSPAAdminRoutes 挂载全部 JSON 端点。
//
// 判权语义刻意与 SSR 的 requireAdmin 逐条对齐：未登录 401、非 admin 403 并写
// permission.denied 审计、bearer/API Key 不属于浏览器会话一律 403。授权判定只在服务端，
// 前端拿不到任何未授权数据。

// spaAdminPage 返回一个 GET 处理器：SPA 已加载时发应用壳，否则调用原有 SSR 处理器。
//
// 它不改变任何判权：调用它的路由仍然先经过 requireAdmin（未登录 303 到登录页、
// 非 admin 403），因此非管理员永远拿不到应用壳（DESIGN.md §8.5）。
func (s *Server) spaAdminPage(ssr gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.spa != nil {
			s.spa.ServeIndex(c)
			return
		}
		ssr(c)
	}
}

// spaAdminError 写出管理 JSON 端点的错误包壳：code 稳定且英文，message 为英文兜底文案，
// 前端按 code 映射本地化提示（DESIGN.md §7.3、§8.3）。
func spaAdminError(c *gin.Context, status int, code string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{
		"code":    code,
		"message": api.ErrorMessage(c.Request.Context(), code),
	}})
}

// spaAdminGuard 是 /api/v1/admin/* 的唯一入口守卫，与 requireAdmin 同源同判：
// 未登录（或已禁用）401、非 admin 403（并写越权审计）、bearer/API Key 403。
func (s *Server) spaAdminGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		// bearer 与 API Key 凭据不属于浏览器会话，管理端点只接受会话 cookie。
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.GetHeader("Authorization"))), "bearer ") {
			spaAdminError(c, http.StatusForbidden, api.CodeForbidden)
			c.Abort()
			return
		}
		if _, ok := api.CurrentAPIKey(c); ok {
			spaAdminError(c, http.StatusForbidden, api.CodeForbidden)
			c.Abort()
			return
		}
		u, ok := auth.CurrentUser(c)
		if !ok || u.Status != store.StatusActive {
			spaAdminError(c, http.StatusUnauthorized, api.CodeUnauthorized)
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
			spaAdminError(c, http.StatusForbidden, api.CodeForbidden)
			c.Abort()
			return
		}
		c.Next()
	}
}

// parseUintParam 解析路径里的十进制 id；0 与非法输入一律返回错误（id 从 1 起）。
func parseUintParam(raw string) (uint64, error) {
	id, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if err != nil || id == 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}

// registerSPAAdminRoutes 挂载管理面板的 JSON 端点（读 + 写）。
//
// 依赖未装配时整体跳过，保证 M0 阶段与未启用会话的测试仍能构造 Server。写操作一律过会话
// CSRF（DESIGN.md §4.3）。SSR 的 /admin/* 表单端点保持原样，SPA 因此不遮蔽任何既有路径。
func (s *Server) registerSPAAdminRoutes(router *gin.Engine) {
	if s.sessions == nil || s.users == nil {
		return
	}
	g := router.Group("/api/v1/admin")
	g.Use(s.spaAdminGuard())

	// 只读端点（概览 / 健康 / 审计）。
	g.GET("/summary", s.spaAdminSummary)
	g.GET("/health", s.spaAdminHealth)
	g.GET("/audit", s.spaAdminAudit)

	// 用户管理（列表读 + 危险动作写；写操作过会话 CSRF）。
	g.GET("/users", s.spaAdminUsers)
	g.POST("/users", s.sessions.CSRFMiddleware(), s.spaAdminUserCreate)
	g.POST("/users/:id/status", s.sessions.CSRFMiddleware(), s.spaAdminUserStatus)
	g.POST("/users/:id/role", s.sessions.CSRFMiddleware(), s.spaAdminUserRole)
	g.POST("/users/:id/password", s.sessions.CSRFMiddleware(), s.spaAdminUserResetPassword)
	g.POST("/users/:id/logout", s.sessions.CSRFMiddleware(), s.spaAdminUserForceLogout)
	g.POST("/users/:id/delete", s.sessions.CSRFMiddleware(), s.spaAdminUserDelete)

	// 注册与邀请。
	g.GET("/registration", s.spaAdminRegistration)
	g.POST("/registration", s.sessions.CSRFMiddleware(), s.spaAdminRegistrationSave)
	g.POST("/invites", s.sessions.CSRFMiddleware(), s.spaAdminInviteCreate)
	g.POST("/invites/:id/revoke", s.sessions.CSRFMiddleware(), s.spaAdminInviteRevoke)

	// API Key 总览（读 + 撤销）。
	g.GET("/api-keys", s.spaAdminAPIKeys)
	g.POST("/api-keys/:id/revoke", s.sessions.CSRFMiddleware(), s.spaAdminAPIKeyRevoke)

	// 系统设置（读 + 写；敏感键经加密落库）。
	g.GET("/settings", s.spaAdminSettings)
	g.POST("/settings", s.sessions.CSRFMiddleware(), s.spaAdminSettingsSave)

	// 邮件（SMTP）配置与测试连接。
	g.GET("/smtp", s.spaAdminSMTP)
	g.POST("/smtp", s.sessions.CSRFMiddleware(), s.spaAdminSMTPSave)
	g.POST("/smtp/test", s.sessions.CSRFMiddleware(), s.spaAdminSMTPTest)

	// 身份与 OIDC 配置、测试连接与解绑。
	g.GET("/oidc", s.spaAdminOIDC)
	g.POST("/oidc", s.sessions.CSRFMiddleware(), s.spaAdminOIDCSave)
	g.POST("/oidc/test", s.sessions.CSRFMiddleware(), s.spaAdminOIDCTest)
	g.POST("/oidc/identities/:id/unlink", s.sessions.CSRFMiddleware(), s.spaAdminOIDCUnlink)

	// 作业列表与取消。
	g.GET("/jobs", s.spaAdminJobs)
	g.POST("/jobs/:id/cancel", s.sessions.CSRFMiddleware(), s.spaAdminJobCancel)

	// 语言包完整度报告（只读）。
	g.GET("/i18n", s.spaAdminI18n)
}
