package web

import (
	"github.com/gin-gonic/gin"
)

// registerSettingsRoutes mounts the personal settings pages.
//
// These are the logged-in user's own pages, and the routes are not under /admin/*: any logged-in user can change their own
// display name, UI language, timezone, review day cutoff and password. The SSR page layer is gone: /settings, /settings/security,
// /settings/email and /settings/keys only serve the SPA app shell, and all reads and writes go through the /api/v1 JSON endpoints.
// Skips when the dependencies are not wired, so M0-stage tests can still construct a Server.
func (s *Server) registerSettingsRoutes(router *gin.Engine) {
	if s.sessions == nil || s.users == nil || s.accounts == nil {
		return
	}
	router.GET("/settings", s.settingsRoute)
	// Security: password change and two-step verification share one page; the password goes through /api/v1/settings/password, two-step verification through
	// /api/v1/settings/totp* (totp_api.go). The page itself uses the same criterion as /settings: logged in only.
	router.GET("/settings/security", s.settingsRoute)
	// Per-user API key management: the routes and handlers are in keys.go.
	router.GET("/settings/keys", s.keysRoute)
}

// settingsRoute serves GET /settings: it only serves the SPA app shell,
// and the client router renders the personal settings page; profile/password/language reads and writes go through the JSON endpoints
// /api/v1/profile, /api/v1/settings/locale and /api/v1/settings/password.
//
// Authorization matches the pre-migration SSR settings page item by item: this is the logged-in user's own page, only a logged-in session is required, and a logged-out visitor is always
// redirected to the login page; the page itself does not distinguish roles. The SSR page layer is gone, so it no longer falls back to a settings page.
func (s *Server) settingsRoute(c *gin.Context) {
	if _, ok := s.requireUser(c); !ok {
		return
	}
	s.shell.ServeIndex(c)
}

// supportedLocale reports whether code is a currently supported language code.
// Still reused by the SPA's JSON endpoints (profile.go, admin_settings_api.go) and by locale resolution.
func (s *Server) supportedLocale(code string) bool {
	for _, c := range s.i18n.SupportedCodes() {
		if c == code {
			return true
		}
	}
	return false
}
