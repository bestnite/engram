package web

import (
	"github.com/gin-gonic/gin"
)

// presetRoute 提供 GET /presets：只发 SPA 应用壳，
// 由客户端路由渲染调度预设页，读写走 /api/v1/presets* 的 JSON 端点。
//
// 判权与迁移前的 SSR 预设页逐项一致：只要求已登录会话，未登录一律重定向登录页。
// SSR 页面层已删除，不再回退预设页。
func (s *Server) presetRoute(c *gin.Context) {
	if _, ok := s.requireUser(c); !ok {
		return
	}
	s.spa.ServeIndex(c)
}
