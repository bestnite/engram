package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"

	"github.com/gin-gonic/gin"
)

// CSRFDoubleSubmitCookieName 是会话前表单（/setup、/login、/register）双提交 cookie 的名字。
// 这些表单提交时还没有服务端会话，会话绑定的 CSRF token 无从产生，因此改用
// 双提交 cookie：GET 时下发一个随机 token 的 cookie，同时把同一 token 镜像进表单隐藏字段，
// 提交时比较两者（DESIGN.md §4.3、§11；B-13）。
const CSRFDoubleSubmitCookieName = "csrf_double"

// doubleSubmitMinLen 是接受一个 cookie token 所需的最小长度；短于它的值一律视为非法并重新生成。
// 生成的 token 是 32 字节随机数的 base64url（43 字符），这个下限只用于拒绝明显的垃圾值。
const doubleSubmitMinLen = 16

// newDoubleSubmitToken 生成一个新的会话前 CSRF token（32 字节随机数，URL 安全编码）。
func newDoubleSubmitToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// validDoubleSubmitToken 判定 cookie 里的值是否可用；空串与过短值都不接受。
func validDoubleSubmitToken(v string) bool {
	return len(v) >= doubleSubmitMinLen
}

// EnsureDoubleSubmitToken 返回本次请求应镜像进表单的会话前 CSRF token。
//
// 已有一个合法的 csrf_double cookie 时直接复用（并保持响应不再重复下发）；
// 否则生成新 token 并写入 cookie。
//
// cookie 设为 HttpOnly：表单由服务端渲染时把同一 token 写进隐藏字段，前端脚本无需读取它。
// SameSite=Lax 与 Secure（仅 TLS）按会话 cookie 的同一套策略设置。
func EnsureDoubleSubmitToken(c *gin.Context) string {
	if v, err := c.Cookie(CSRFDoubleSubmitCookieName); err == nil && validDoubleSubmitToken(v) {
		return v
	}
	tok, err := newDoubleSubmitToken()
	if err != nil {
		// 生成失败时返回空串：表单不带 token，提交会被中间件拒绝（fail closed，绝不放行）。
		return ""
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     CSRFDoubleSubmitCookieName,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		Secure:   c.Request.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   24 * 60 * 60,
	})
	return tok
}

// DoubleSubmitMiddleware 校验会话前表单的双提交 cookie，与 SameSite=Lax 形成双保险。
//
// 与 Manager.CSRFMiddleware 的关键区别：它不依赖服务端会话，因此可用于登录前的 POST。
// 已有会话的写请求仍走会话绑定的 CSRFMiddleware，两条路径互不影响。
//
// 校验规则：非 GET/HEAD/OPTIONS 请求必须同时携带 csrf_double cookie 与镜像 token 的请求值
// （表单字段 csrf_token 或头部 X-CSRF-Token），两者一致才放行；缺任意一侧或值不一致返回 403。
func DoubleSubmitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		cookie, err := c.Cookie(CSRFDoubleSubmitCookieName)
		token := c.GetHeader(CSRFHeaderName)
		if token == "" {
			// PostForm 会解析并缓存表单体，不会与后续 handler 的读取冲突。
			token = c.PostForm(CSRFFieldName)
		}
		if err != nil || !validDoubleSubmitToken(cookie) || token == "" ||
			subtle.ConstantTimeCompare([]byte(cookie), []byte(token)) != 1 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": gin.H{"code": "csrf_failed", "message": "invalid or missing CSRF token"},
			})
			return
		}
		c.Next()
	}
}
