package web

import (
	"github.com/gin-gonic/gin"
)

// 本文件是 Web 层入口：邮件偏好页（/settings/notifications）只发 SPA 应用壳。
//
// 偏好目录与发信方共用 internal/mail 的目录；页面读取与保存走
// /api/v1/settings/notifications 的 JSON 端点（mail_prefs_api.go），SSR 页面层已删除。

// registerMailPrefsRoutes 挂载邮件偏好页路由。
// 依赖未装配时跳过，保证 M0 阶段的测试仍能构造 Server。
func (s *Server) registerMailPrefsRoutes(router *gin.Engine) {
	if s.sessions == nil || s.users == nil {
		return
	}
	router.GET("/settings/notifications", s.mailPrefsRoute)
}

// mailPrefsRoute 提供 GET /settings/notifications：只发 SPA 应用壳，
// 由客户端路由渲染邮件通知偏好页；读取与写入走 /api/v1/settings/notifications（同一份服务逻辑）。
// 授权判定与迁移前一致：未登录一律重定向登录页。SSR 页面层已删除，不再回退偏好页。
func (s *Server) mailPrefsRoute(c *gin.Context) {
	if _, ok := s.requireUser(c); !ok {
		return
	}
	s.shell.ServeIndex(c)
}
