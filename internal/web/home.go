package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// homeRoute 提供 GET /（首页「今日」）。两条分支互斥，书写顺序即优先级：
//
//  1. 首启窗口（M1-25）：还没有活跃管理员时首页没有任何可展示的内容，把访客直接送去引导页，
//     连 SPA 外壳都不给。`/setup` 在管理员出现后自身 404（见 setupPage），所以这条重定向只在
//     首启窗口内生效，不会变成常驻跳转，也不会与 SPA 首页形成循环——判定只看管理员数量，
//     与是否登录无关，未登录访客在管理员已存在时不会被再次送回 `/setup`。
//  2. 其余情形：返回 SPA 应用壳，由客户端路由渲染首页，数据走 JSON 端点。
func (s *Server) homeRoute(c *gin.Context) {
	// 不带 ?lang：语言由引导页按 Accept-Language 与 cookie 自行解析。
	// Users 依赖缺失时不妄断（判定会 panic），退回应用壳路径。
	if s.users != nil && s.setupAvailable(c) {
		c.Redirect(http.StatusSeeOther, "/setup")
		return
	}
	s.shell.ServeIndex(c)
}
