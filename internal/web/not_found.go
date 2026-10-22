package web

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
)

// registerNotFoundRoute mounts the unknown-path fallback (NoRoute).
// It must be called after the other routes are registered: NoRoute only fires when no route matches, and registration order does not change
// the priority of registered routes, but putting the fallback last stops readers from thinking it shadows the other routes.
func (s *Server) registerNotFoundRoute(router *gin.Engine) {
	router.NoRoute(s.notFound)
}

// notFound is the catch-all fallback for a request that matches no route (gin NoRoute semantics guarantee it does not shadow registered routes).
//
// Four exits, so the shape cannot drift (ROADMAP Task B2):
//   - /api subpaths: back to internal/api's uniform JSON error envelope; script clients always get JSON;
//   - /mcp subpaths and non-GET requests: a plain 404 status only, with no HTML stuffed in;
//   - unmatched static/media asset paths (/assets/, /static/, /media/ etc.): 404 status only, never fall back to HTML;
//   - any other unknown page-type GET path: fall back to the SPA app shell (index.html) and let the client router take over.
func (s *Server) notFound(c *gin.Context) {
	path := c.Request.URL.Path
	switch {
	case isAPIPath(path):
		s.apiNotFound(c)
	case c.Request.Method != http.MethodGet || isMCPPath(path):
		c.AbortWithStatus(http.StatusNotFound)
	case isStaticOrMediaPath(path):
		c.AbortWithStatus(http.StatusNotFound)
	default:
		s.shell.ServeIndex(c)
	}
}

// apiNotFound returns the uniform error envelope for unknown /api subpaths, with the same field shape as internal/api's abortError
// ({"error":{"code","message"}}); message goes through the same stable English exit.
func (s *Server) apiNotFound(c *gin.Context) {
	code := api.CodeNotFound
	c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": gin.H{
		"code":    code,
		"message": api.ErrorMessage(c.Request.Context(), code),
	}})
}

// isAPIPath reports whether the path belongs to the REST API (/api and /api/...).
func isAPIPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/")
}

// isMCPPath reports whether the path belongs to the built-in MCP endpoint.
func isMCPPath(path string) bool {
	return path == "/mcp" || strings.HasPrefix(path, "/mcp/")
}

// isStaticOrMediaPath reports whether the path is a static-asset or media-asset request.
func isStaticOrMediaPath(path string) bool {
	return strings.HasPrefix(path, "/static/") ||
		strings.HasPrefix(path, "/assets/") ||
		strings.HasPrefix(path, "/media/") ||
		path == "/favicon.ico"
}
