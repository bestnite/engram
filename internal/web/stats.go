package web

import (
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// registerStatsRoutes 挂载统计页（M7-3）。
//
// 页面上的每个数字都来自 internal/store 的既有聚合查询（M7-1/M7-2）；handler 不写任何
// 统计 SQL。原因：M7-4 的回溯校验（store.RetroCheck）对照的正是这批查询，一旦 handler
// 另写一套 SQL，页面数字就脱离了可校验的路径。
//
// 依赖未装配时跳过，保证 M0 阶段的测试仍能构造 Server。
func (s *Server) registerStatsRoutes(router *gin.Engine) {
	if s.sessions == nil {
		return
	}
	// GET /stats 返回应用壳，由客户端路由渲染统计页。统计页没有任何写操作，因此这里只动
	// 这一个 GET 路由，其余路由（含 SPA 明细接口）不变。
	router.GET("/stats", s.statsRoute)
	// SPA 统计明细接口：只读、只接受浏览器会话（不走 /api/v1 的 API Key 组），
	// 与统计页共用同一批 store 聚合，口径不会分叉。
	router.GET("/api/v1/stats/detail", s.statsDetail)
}

// statsRoute 提供 GET /stats：返回应用壳，由客户端路由渲染统计页，
// 数据走同一批 store 聚合的 GET /api/v1/stats/detail。
//
// 与迁移前的 SSR 页面一致：先要求已登录会话，未登录一律重定向到登录页，页面迁移不改动授权
// 判定，也不新增任何写路径。
func (s *Server) statsRoute(c *gin.Context) {
	if _, ok := s.requireUser(c); !ok {
		return
	}
	s.shell.ServeIndex(c)
}

// userLocation 解析用户时区；回退规则见 store.LoadLocation（空/非法名 → UTC），
// 页面不会因脏数据 500。
func userLocation(user *store.User) *time.Location {
	return store.LoadLocation(user.Timezone)
}
