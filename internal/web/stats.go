package web

import (
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// registerStatsRoutes mounts the stats page.
//
// Every number on the page comes from the existing aggregate queries in internal/store; the handler writes no
// stats SQL of its own. The reason: the retro check (store.RetroCheck) validates against exactly these queries, and once a handler
// writes its own SQL, the page's numbers leave the verifiable path.
//
// Skips when the dependencies are not wired, so M0-stage tests can still construct a Server.
func (s *Server) registerStatsRoutes(router *gin.Engine) {
	if s.sessions == nil {
		return
	}
	// GET /stats returns the app shell, and the client router renders the stats page. The stats page has no write operations, so only
	// this one GET route changes; the other routes (including the SPA detail endpoint) stay as they are.
	router.GET("/stats", s.statsRoute)
	// The SPA stats detail endpoint: read-only, accepts browser sessions only (not the /api/v1 API key group),
	// and shares the same set of store aggregates as the stats page, so the numbers cannot fork.
	router.GET("/api/v1/stats/detail", s.statsDetail)
}

// statsRoute serves GET /stats: it returns the app shell, the client router renders the stats page,
// and data goes through GET /api/v1/stats/detail backed by the same store aggregates.
//
// Consistent with the pre-migration SSR page: a logged-in session is required first, a logged-out visitor is always redirected to the login page, and the page migration changes neither the authorization
// decision nor adds any write path.
func (s *Server) statsRoute(c *gin.Context) {
	if _, ok := s.requireUser(c); !ok {
		return
	}
	s.shell.ServeIndex(c)
}

// userLocation resolves the user's timezone; the fallback rules are in store.LoadLocation (empty/illegal name → UTC),
// so dirty data cannot make the page 500.
func userLocation(user *store.User) *time.Location {
	return store.LoadLocation(user.Timezone)
}
