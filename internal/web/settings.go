package web

import (
	"github.com/gin-gonic/gin"
)

// registerSettingsRoutes 挂载个人设置页（M1-8）。
//
// 这是登录用户自己的页面，路由不挂在 /admin/* 下：任何已登录用户都能改自己的
// 显示名、界面语言、时区、复习日切点与密码。SSR 页面层已删除：/settings 与
// /settings/keys 只发 SPA 应用壳，资料/密码/语言与 key 的读写全部走 /api/v1 的 JSON 端点。
// 依赖未装配时跳过，保证 M0 阶段的测试仍能构造 Server。
func (s *Server) registerSettingsRoutes(router *gin.Engine) {
	if s.sessions == nil || s.users == nil || s.accounts == nil {
		return
	}
	router.GET("/settings", s.settingsRoute)
	// 用户级 API Key 管理（M4-10）：路由与 handler 在 keys.go。
	router.GET("/settings/keys", s.keysRoute)
	// 邮件类型偏好页（M1-18）：路由与 handler 在 mail_prefs.go，仍属个人设置体系。
	s.registerMailPrefsRoutes(router)
}

// settingsRoute 提供 GET /settings：只发 SPA 应用壳，
// 由客户端路由渲染个人设置页；资料/密码/语言的读写走 /api/v1/profile、
// /api/v1/settings/locale 与 /api/v1/settings/password 的 JSON 端点。
//
// 判权与迁移前的 SSR 设置页逐项一致：这是登录用户自己的页面，只要求已登录会话，未登录一律
// 重定向到登录页；页面本身不区分角色。SSR 页面层已删除，不再回退设置页。
func (s *Server) settingsRoute(c *gin.Context) {
	if _, ok := s.requireUser(c); !ok {
		return
	}
	s.spa.ServeIndex(c)
}

// supportedLocale 报告 code 是否是当前受支持的语言码。
// 仍被 SPA 的 JSON 端点（spa_profile.go、spa_admin_settings.go）与语言解析复用。
func (s *Server) supportedLocale(code string) bool {
	for _, c := range s.i18n.SupportedCodes() {
		if c == code {
			return true
		}
	}
	return false
}
