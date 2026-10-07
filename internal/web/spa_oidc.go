package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// 本文件是 OIDC 可选登录入口的 SPA 同源 JSON 探测端点（DESIGN.md §4.4、§8.1）。
//
// SPA 的登录视图无法在构建期知道服务端是否开启了 OIDC，因此需要一个只读探测端点。
// 它只暴露「是否可用」与稳定的发起地址，绝不回显 issuer / client_id / client_secret 或任何凭据。

// registerSPAOIDCRoutes 挂载 OIDC 登录入口的探测端点（登录前流程，无需会话）。
func (s *Server) registerSPAOIDCRoutes(router *gin.Engine) {
	router.GET("/api/v1/auth/oidc", s.spaOIDCInfo)
}

// spaOIDCInfo 返回 OIDC 登录入口是否可用与发起地址（GET /api/v1/auth/oidc）。
// enabled 的判定与 oidcStart 的可达性同源（oidcLoadConfig + cfg.Usable），
// start_url 与 OIDC 登录按钮的 href 一致（/auth/oidc/start），不会出现「页面写着 A、实际发 B」的漂移。
func (s *Server) spaOIDCInfo(c *gin.Context) {
	enabled := false
	if cfg, err := s.oidcLoadConfig(c); err == nil && cfg.Usable() {
		enabled = true
	}
	c.JSON(http.StatusOK, gin.H{
		"enabled":   enabled,
		"start_url": "/auth/oidc/start",
	})
}
