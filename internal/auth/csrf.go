package auth

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
)

// CSRFFieldName 是表单里承载 token 的字段名；头部用 X-CSRF-Token。
const CSRFFieldName = "csrf_token"

// CSRFHeaderName 是非表单请求（htmx / fetch）携带 token 的头部。
const CSRFHeaderName = "X-CSRF-Token"

// CSRFMiddleware 校验所有非 GET 请求的 CSRF token，与 SameSite=Lax 形成双保险。
// token 绑定在服务端会话上，所以必须先跑 Manager.Middleware；没有有效会话一律 403。
func (m *Manager) CSRFMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		sess, ok := CurrentSession(c)
		if !ok {
			// 无会话即无 token 来源，直接拒绝，避免把 CSRF 校验降级成"无会话即放行"。
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": gin.H{"code": "csrf_no_session", "message": "no active session for CSRF validation"},
			})
			return
		}
		token := c.GetHeader(CSRFHeaderName)
		if token == "" {
			// PostForm 会解析并缓存表单体，不会与后续 handler 的读取冲突。
			token = c.PostForm(CSRFFieldName)
		}
		if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(sess.CSRFToken)) != 1 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": gin.H{"code": "csrf_failed", "message": "invalid or missing CSRF token"},
			})
			return
		}
		c.Next()
	}
}
