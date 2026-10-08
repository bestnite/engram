package web

import (
	"github.com/gin-gonic/gin"
)

// 用户级 API Key 管理页。
//
// 与 /admin/api-keys 的分工：管理面板是全用户总览（可撤销、不能创建）；/settings/keys 是登录
// 用户自己的 key 列表。SSR 页面层已删除：这里只发 SPA 应用壳，列表/创建/撤销走
// /api/v1/keys 的 JSON 端点（会话鉴权，见 internal/api）。安全底线不变：
//   - 一律按当前会话用户的 id 取列表与撤销，别人的 key 既不列出也不可撤销。
//   - 明文只在创建成功的那次响应里出现一次，绝不落库、绝不在后续 GET 出现。
//   - 创建与撤销都是写操作，过 CSRF，并各写一条审计。

// keysRoute 提供 GET /settings/keys：只发 SPA 应用壳，
// 由客户端路由渲染「我的 API Key」页，列表/创建/撤销走 /api/v1/keys 的 JSON 端点。
//
// 判权与迁移前的 SSR 页逐项一致：先要求已登录会话（匿名重定向登录页），key 一律按当前会话
// 用户的 id 取，不因切壳而放开。SSR 页面层已删除，不再回退 keys 页。
func (s *Server) keysRoute(c *gin.Context) {
	if _, ok := s.requireUser(c); !ok {
		return
	}
	s.shell.ServeIndex(c)
}
