package web

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
)

// registerNotFoundRoute 挂上未知路径回退（NoRoute）。
// 必须在其余路由注册之后调用：NoRoute 只在没有任何路由匹配时才触发，注册顺序不改变
// 已注册路由的优先级，但把回退放在最后能避免读代码时误以为它会遮蔽其它路由。
func (s *Server) registerNotFoundRoute(router *gin.Engine) {
	router.NoRoute(s.notFound)
}

// notFound 是未命中任何路由的 catchall 回退（gin NoRoute 语义保证它不会遮蔽已注册路由）。
//
// 分四种出口，避免形态漂移（ROADMAP Task B2）：
//   - /api 子路径：回 internal/api 的统一 JSON 错误包壳，脚本客户端永远拿到 JSON；
//   - /mcp 子路径与非 GET 请求：只回朴素的 404 状态，不塞 HTML；
//   - 静态/媒体资源未命中路径（/assets/、/static/、/media/ 等）：只回 404 状态，严禁回退 HTML；
//   - 其余未知的页面型 GET 路径：回退 SPA 应用壳（index.html），由客户端路由接管。
func (s *Server) notFound(c *gin.Context) {
	path := c.Request.URL.Path
	switch {
	case isAPIPath(path):
		s.apiNotFound(c)
	case c.Request.Method != http.MethodGet || isMCPPath(path):
		c.AbortWithStatus(http.StatusNotFound)
	case isStaticOrMediaPath(path):
		c.AbortWithStatus(http.StatusNotFound)
	default:
		s.shell.ServeIndex(c)
	}
}

// apiNotFound 给未知的 /api 子路径回统一错误包壳，字段形态与 internal/api 的 abortError
// 一致（{"error":{"code","message"}}）；message 走同一份稳定英文出口。
func (s *Server) apiNotFound(c *gin.Context) {
	code := api.CodeNotFound
	c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": gin.H{
		"code":    code,
		"message": api.ErrorMessage(c.Request.Context(), code),
	}})
}

// isAPIPath 判断路径是否属于 REST API（/api 与 /api/...）。
func isAPIPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/")
}

// isMCPPath 判断路径是否属于内置 MCP 端点。
func isMCPPath(path string) bool {
	return path == "/mcp" || strings.HasPrefix(path, "/mcp/")
}

// isStaticOrMediaPath 判断路径是否属于静态资源或媒体资源请求。
func isStaticOrMediaPath(path string) bool {
	return strings.HasPrefix(path, "/static/") ||
		strings.HasPrefix(path, "/assets/") ||
		strings.HasPrefix(path, "/media/") ||
		path == "/favicon.ico"
}
