package web

import "github.com/gin-gonic/gin"

// registerHelpRoutes mounts the static help pages.
//
// Skips when the dependencies are not wired, so M0-stage tests can still construct a Server.
func (s *Server) registerHelpRoutes(router *gin.Engine) {
	if s.sessions == nil {
		return
	}
	// The page holds static documentation and reads no data, but it is still registered explicitly rather than
	// left to the unknown-path fallback: that fallback hands the app shell to any page GET, including a
	// logged-out visitor, while every other app page requires a session first. The explicit route keeps one
	// reachability rule, and the shared page-route test walks it.
	router.GET("/help/shortcodes", s.shortcodesHelpRoute)
}

// shortcodesHelpRoute serves GET /help/shortcodes: it returns the app shell and the client router renders the page.
//
// The page serves no data and has no write path, yet it still requires a signed-in session, because every
// navigable app page follows the same reachability contract: a logged-out visitor is redirected to the login
// page rather than seeing a page the rest of the app would hide from them.
func (s *Server) shortcodesHelpRoute(c *gin.Context) {
	if _, ok := s.requireUser(c); !ok {
		return
	}
	s.shell.ServeIndex(c)
}
