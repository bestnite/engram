package web

import (
	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// 管理面板的用户管理支撑函数。
//
// SSR 用户管理页删除后，用户管理的读写在 /api/v1/admin/users* 的 JSON 端点上
// （admin_users_api.go）；这里只保留 JSON 路径仍复用的判定。

// wouldRemoveLastAdmin 判断对 target 做「禁用 / 删除 / 降权」是否会清空管理员。
// 仅在 target 本身是活跃管理员、且系统里没有第二个活跃管理员时为 true。
func (s *Server) wouldRemoveLastAdmin(c *gin.Context, target *store.User) bool {
	if target.Role != store.RoleAdmin || target.Status != store.StatusActive {
		return false
	}
	n, err := s.users.CountActiveAdmins(c.Request.Context())
	if err != nil {
		s.logger.Error("admin: count active admins failed", "error", err)
		return true // 读不出计数时保守拒绝，避免误锁死系统
	}
	return n <= 1
}
