package web

import (
	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// registerSharingRoutes mounts the sharing management page.
//
// The sharing management page is open to the owner only (decided via auth.DeckAccess). GET returns the app shell; every write goes through
// the JSON endpoints under /api/v1/decks/:id/sharing and passes the CSRF middleware.
// Skips when the dependencies are not wired, so M0-stage tests can still construct a Server.
func (s *Server) registerSharingRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.grants == nil || s.users == nil {
		return
	}
	// GET /decks/:id/sharing returns the app shell, the client router renders the sharing management page, and data goes through the JSON endpoints below.
	router.GET("/decks/:id/sharing", s.sharingPageRoute)
	// The SPA's sharing read/write endpoints (sharing_api.go): data still goes through the same set of store methods.
	router.GET("/api/v1/decks/:id/sharing", s.sharingGet)
	router.POST("/api/v1/decks/:id/sharing/grants", s.sessions.CSRFMiddleware(), s.sharingGrant)
	router.PATCH("/api/v1/decks/:id/sharing/grants/:userID", s.sessions.CSRFMiddleware(), s.sharingGrant)
	router.DELETE("/api/v1/decks/:id/sharing/grants/:userID", s.sessions.CSRFMiddleware(), s.sharingRevoke)
	// Share consent model (L3): pending invites, accept/reject, and the user's own receiving policy.
	s.registerShareInviteRoutes(router)
	router.POST("/api/v1/decks/:id/sharing/links", s.sessions.CSRFMiddleware(), s.sharingLinkCreate)
	router.DELETE("/api/v1/decks/:id/sharing/links/revoke/:digest", s.sessions.CSRFMiddleware(), s.sharingLinkRevoke)
	router.DELETE("/api/v1/decks/:id/sharing/links", s.sessions.CSRFMiddleware(), s.sharingLinkRevokeAll)
}

// sharingPageRoute serves GET /decks/:id/sharing: it returns the app shell,
// the client router renders the sharing management page, and data still goes through the existing JSON endpoints.
//
// Authorization matches the pre-migration SSR sharing page item by item: first a logged-in session is required (anonymous users are redirected to login), then deck ownership is decided
// by the owner role — editor/reader and stranger users still get 403, and the shell switch does not hand the sharing shell to an unauthorized user.
func (s *Server) sharingPageRoute(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	deckID, ok := s.deckIDParam(c)
	if !ok {
		return
	}
	if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleOwner); !ok {
		return
	}
	s.shell.ServeIndex(c)
}

// shortDigest truncates the token digest for list display; a digest cannot be reversed, so showing a prefix does not leak the secret.
func shortDigest(digest string) string {
	if len(digest) <= 12 {
		return digest
	}
	return digest[:12] + "…"
}

// shareLinkPasswordHasher is the hasher for share link passwords.
// A password is "a lock added when forwarding the link to someone", not an account master password, so it reuses the project's unified argon2id encoding format,
// with parameters starting from DefaultParams; a future strength upgrade only affects new passwords, and old hashes still verify with their own parameters.
var shareLinkPasswordHasher = auth.DefaultPasswordHasher()
