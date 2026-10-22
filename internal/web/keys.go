package web

import (
	"github.com/gin-gonic/gin"
)

// User-level API key management page.
//
// Division of labour with /admin/api-keys: the admin panel is an all-user overview (can revoke, cannot create); /settings/keys is the signed-in
// user's own key list. The SSR page layer has been deleted: this only sends the SPA app shell, while list/create/revoke go through
// the /api/v1/keys JSON endpoints (session auth, see internal/api). The security baseline is unchanged:
//   - list and revoke always go by the current session user's id; other people's keys are neither listed nor revocable.
//   - the plaintext appears only once, in the response of a successful creation; never persisted, never in a later GET.
//   - create and revoke are both writes, go through CSRF, and each writes one audit record.

// keysRoute serves GET /settings/keys: it only sends the SPA app shell,
// the client router renders the "My API keys" page, and list/create/revoke go through the /api/v1/keys JSON endpoints.
//
// Authorization matches the pre-migration SSR page item by item: a signed-in session is required first (anonymous visitors are redirected to the login page), and keys always go by the current session
// user's id, not loosened by the shell switch. The SSR page layer has been deleted, so there is no fallback to the keys page.
func (s *Server) keysRoute(c *gin.Context) {
	if _, ok := s.requireUser(c); !ok {
		return
	}
	s.shell.ServeIndex(c)
}
