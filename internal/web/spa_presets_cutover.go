package web

import (
	"github.com/gin-gonic/gin"
)

// presetRoute 提供 GET /presets：SPA 已加载时返回应用壳（DESIGN.md §8.5），
// 由客户端路由渲染调度预设页，读写走 /api/v1/presets* 的 JSON 端点（DESIGN.md §8.1）。
//
// 判权与迁移前的 SSR 预设页逐项一致：只要求已登录会话，未登录一律重定向登录页。
// SPA 缺失（降级构建）时回退 SSR 预设页 presetList，模板与全部写路径保持不变。
func (s *Server) presetRoute(c *gin.Context) {
	if _, ok := s.requireUser(c); !ok {
		return
	}
	if s.spa != nil {
		s.spa.ServeIndex(c)
		return
	}
	s.presetList(c)
}
