package web

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/store"
)

// Admin panel entry point.
//
// Hard constraint: only role = admin gets in; everyone else is always 403. The SSR page layer has been removed: every GET route first passes
// requireAdmin, then serves the SPA app shell; the panel's reads and writes live on the /api/v1/admin/* JSON endpoints (the files starting at admin_api.go).

// adminRoute describes one implemented admin route; registration and the "non-admin is always 403" test share the same list,
// so the two cannot drift apart.
type adminRoute struct {
	Method string
	Path   string
}

// adminRoutes is the full set of currently registered /admin/* routes (after the SSR page layer was removed only GET page shells remain).
// Add a line here when adding a subpage; the test verifies one by one that non-admin access is rejected.
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

// registerAdminRoutes mounts the admin panel's page shells and JSON endpoints.
// Skipped when sessions are not wired up, keeping M0-stage tests constructible.
func (s *Server) registerAdminRoutes(router *gin.Engine) {
	if s.sessions == nil {
		return
	}
	// Every page route is requireAdmin + app shell: requireAdmin runs first, so non-admins never get the shell.
	for _, r := range adminRoutes() {
		router.Handle(r.Method, r.Path, s.requireAdmin(), s.adminShell)
	}
	// The SPA's JSON endpoints (/api/v1/admin/*); authorization is decided in adminGuard.
	s.registerAdminAPIRoutes(router)
}

// requireAdmin is the single entry guard for the admin panel: signed-out requests go back to the login page, non-admins are always 403.
func (s *Server) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		u, ok := auth.CurrentUser(c)
		if !ok {
			// Signed out: with no session there is no CSRF source either, so go back to the login page rather than exposing 403 semantics.
			c.Redirect(http.StatusSeeOther, "/login")
			c.Abort()
			return
		}
		if u.Role != store.RoleAdmin {
			// Record privilege-escalation attempts: once several people share an account this is the key clue for working out "who is trying".
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

// siteName returns the effective site name: the settings table, then the language pack's app.name (mail.Composer decides).
func (s *Server) siteName(ctx context.Context, loc *i18n.Localizer) string {
	return s.mailer.SiteName(ctx, loc)
}

// siteDefaultLocale returns the site default locale configured by the admin; empty when unset or unsupported.
// In locale resolution it is only the last fallback level and never overrides ?lang / user settings / Accept-Language.
func (s *Server) siteDefaultLocale(ctx context.Context) string {
	return s.mailer.SiteDefaultLocale(ctx)
}
