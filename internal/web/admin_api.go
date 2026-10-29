package web

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the shared wiring for the admin panel:
//   - adminShell serves the app shell for admin panel GET pages (requireAdmin has already run before it);
//   - adminGuard decides authorization for the /api/v1/admin/* JSON endpoints equivalently to requireAdmin,
//     but responds with JSON 401/403 (a browser fetch cannot follow a 303 to the login page to get HTML);
//   - registerAdminAPIRoutes mounts all the JSON endpoints.
//
// Authorization semantics align with the page guard requireAdmin item by item: 401 when signed out, 403 plus a
// permission.denied audit row for non-admins, and always 403 for bearer/API Key since those are not browser sessions. Authorization is decided server-side only,
// so the frontend receives no unauthorized data.

// adminShell is the handler for every admin panel GET page route: requireAdmin has already run before it
// (303 to the login page when signed out, 403 for non-admins), and this only serves the SPA app shell.
// The SSR page layer has been removed, so no SSR page is served as a fallback any more.
func (s *Server) adminShell(c *gin.Context) {
	s.shell.ServeIndex(c)
}

// adminError writes the error envelope for admin JSON endpoints: code is stable and English, message is English fallback copy,
// and the frontend maps localized hints by code.
func adminError(c *gin.Context, status int, code string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{
		"code":    code,
		"message": api.ErrorMessage(c.Request.Context(), code),
	}})
}

// adminGuard is the single entry guard for /api/v1/admin/*, with the same origin and the same decisions as requireAdmin:
// 401 when signed out (or disabled), 403 for non-admins (plus a privilege-escalation audit row), 403 for bearer/API Key.
func (s *Server) adminGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		// bearer and API Key credentials are not browser sessions; admin endpoints accept session cookies only.
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
			// Record privilege-escalation attempts: exactly the same audit as requireAdmin, making it easy to work out "who is trying".
			s.auditor.Record(c.Request.Context(), store.AuditEntry{
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

// registerAdminAPIRoutes mounts the admin panel's JSON endpoints (read + write).
//
// Skipped entirely when dependencies are not wired up, so M0-stage tests and tests without sessions can still construct Server. Write operations always go through session
// CSRF. The admin panel's reads and writes are only this batch of JSON endpoints (the SSR form endpoints have been removed).
func (s *Server) registerAdminAPIRoutes(router *gin.Engine) {
	if s.sessions == nil || s.users == nil {
		return
	}
	g := router.Group("/api/v1/admin")
	g.Use(s.adminGuard())

	// Read-only endpoints (overview / health / audit).
	g.GET("/summary", s.adminSummary)
	g.GET("/health", s.adminHealth)
	g.GET("/audit", s.adminAudit)

	// User management (list reads + writes for dangerous actions; writes go through session CSRF).
	g.GET("/users", s.adminUsers)
	g.POST("/users", s.sessions.CSRFMiddleware(), s.adminUserCreate)
	g.POST("/users/:id/status", s.sessions.CSRFMiddleware(), s.adminUserStatus)
	g.POST("/users/:id/role", s.sessions.CSRFMiddleware(), s.adminUserRole)
	g.POST("/users/:id/password", s.sessions.CSRFMiddleware(), s.adminUserResetPassword)
	g.POST("/users/:id/logout", s.sessions.CSRFMiddleware(), s.adminUserForceLogout)
	g.POST("/users/:id/delete", s.sessions.CSRFMiddleware(), s.adminUserDelete)

	// Mail templates: list reads + save / delete / preview / test send (writes go through CSRF).
	g.GET("/mail-templates", s.adminMailTemplates)
	g.PUT("/mail-templates/:type/:locale", s.sessions.CSRFMiddleware(), s.adminMailTemplateSave)
	g.DELETE("/mail-templates/:type/:locale", s.sessions.CSRFMiddleware(), s.adminMailTemplateDelete)
	g.POST("/mail-templates/preview", s.sessions.CSRFMiddleware(), s.adminMailTemplatePreview)
	g.POST("/mail-templates/test", s.sessions.CSRFMiddleware(), s.adminMailTemplateTest)

	// Registration and invites.
	g.GET("/registration", s.adminRegistration)
	g.POST("/registration", s.sessions.CSRFMiddleware(), s.adminRegistrationSave)
	g.POST("/invites", s.sessions.CSRFMiddleware(), s.adminInviteCreate)
	g.POST("/invites/:id/revoke", s.sessions.CSRFMiddleware(), s.adminInviteRevoke)

	// API Key overview (read + revoke).
	g.GET("/api-keys", s.adminAPIKeys)
	g.POST("/api-keys/:id/revoke", s.sessions.CSRFMiddleware(), s.adminAPIKeyRevoke)

	// System settings (read + write; sensitive keys are encrypted before being stored).
	g.GET("/settings", s.adminSettings)
	g.POST("/settings", s.sessions.CSRFMiddleware(), s.adminSettingsSave)

	// Mail (SMTP) configuration and connection test.
	g.GET("/smtp", s.adminSMTP)
	g.POST("/smtp", s.sessions.CSRFMiddleware(), s.adminSMTPSave)
	g.POST("/smtp/test", s.sessions.CSRFMiddleware(), s.adminSMTPTest)

	// Identity and OIDC configuration, connection test and unlinking.
	g.GET("/oidc", s.adminOIDC)
	g.POST("/oidc", s.sessions.CSRFMiddleware(), s.adminOIDCSave)
	g.POST("/oidc/test", s.sessions.CSRFMiddleware(), s.adminOIDCTest)
	g.POST("/oidc/identities/:id/unlink", s.sessions.CSRFMiddleware(), s.adminOIDCUnlink)

	// Job list and cancellation.
	g.GET("/jobs", s.adminJobs)
	g.POST("/jobs/:id/cancel", s.sessions.CSRFMiddleware(), s.adminJobCancel)

	// Language pack completeness report (read-only).
	g.GET("/i18n", s.adminI18n)
}
