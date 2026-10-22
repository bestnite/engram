package web

import (
	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// Support functions for the admin panel's user management.
//
// After the SSR user management page was removed, user management reads and writes live on the /api/v1/admin/users* JSON endpoints
// (admin_users_api.go); only the decisions the JSON path still reuses are kept here.

// wouldRemoveLastAdmin reports whether "disabling / deleting / demoting" target would wipe out the admins.
// True only when target is itself an active admin and there is no second active admin in the system.
func (s *Server) wouldRemoveLastAdmin(c *gin.Context, target *store.User) bool {
	if target.Role != store.RoleAdmin || target.Status != store.StatusActive {
		return false
	}
	n, err := s.users.CountActiveAdmins(c.Request.Context())
	if err != nil {
		s.logger.Error("admin: count active admins failed", "error", err)
		return true // Reject conservatively when the count cannot be read, to avoid accidentally locking the system out
	}
	return n <= 1
}
