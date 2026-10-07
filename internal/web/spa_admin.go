package web

import (
	"net/http"
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
}
