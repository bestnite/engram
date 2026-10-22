package web

import (
	"github.com/gin-gonic/gin"
)

// presetRoute serves GET /presets: it only sends the SPA app shell,
// the client router renders the scheduling preset page, and reads/writes go through the /api/v1/presets* JSON endpoints.
//
// Authorization matches the pre-migration SSR preset page item by item: a signed-in session is all it requires, and anonymous visitors are always redirected to the login page.
// The SSR page layer has been deleted, so there is no fallback to the preset page.
func (s *Server) presetRoute(c *gin.Context) {
	if _, ok := s.requireUser(c); !ok {
		return
	}
	s.shell.ServeIndex(c)
}
