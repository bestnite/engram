package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// homeRoute serves GET / (the "Today" home page). The two branches are mutually exclusive, and their written order is their priority:
//
//  1. First-run window: while there is no active admin the home page has nothing to show, so send the visitor straight to the setup page,
//     not even giving them the SPA shell. `/setup` 404s on its own once an admin appears (see setupPage), so this redirect only takes
//     effect inside the first-run window; it never becomes a permanent redirect, and it cannot loop with the SPA home page — the check looks
//     only at the admin count and is independent of login state, so an anonymous visitor is not sent back to `/setup` once an admin exists.
//  2. Everything else: return the SPA app shell; the client router renders the home page and the data comes from JSON endpoints.
func (s *Server) homeRoute(c *gin.Context) {
	// No ?lang: the setup page resolves the language itself from Accept-Language and the cookie.
	// When the Users dependency is missing, do not assume (the check would panic); fall back to the app-shell path.
	if s.users != nil && s.setupAvailable(c) {
		c.Redirect(http.StatusSeeOther, "/setup")
		return
	}
	s.shell.ServeIndex(c)
}
