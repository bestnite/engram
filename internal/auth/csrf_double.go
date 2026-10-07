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
// 提交时比较两者（B-13）。
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
// SameSite=Lax 与 Secure 按会话 cookie 的同一套策略设置：Secure 由调用方依据 BASE_URL 的
// scheme 传入（secure），不能看请求自身的 TLS——生产是反代终止 TLS，应用只收到明文 http，
// 按请求判断会让线上表单 cookie 丢掉 Secure。
func EnsureDoubleSubmitToken(c *gin.Context, secure bool) string {
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
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   24 * 60 * 60,
	})
	return tok
}

// PreSessionCSRFMiddleware 保护「登录前就能调用」的写端点（登录、注册、引导、忘记/重置密码、
// 邮箱验证与改邮箱确认、退订、TOTP 第二步）。
//
// 为什么不能一律用会话绑定校验：这些端点的契约是「匿名也能调」（忘记密码的前提就是登不进去），
// 而匿名时服务端没有会话可存、没有值可比。
//
// 为什么不能一律用双提交：请求可能来自**已登录**的浏览器（例如已登录用户点邮件里的验证/退订链接）。
// 此时浏览器手里只有会话绑定 token，双提交 cookie 与之天然不等，于是这类请求必然 403 —— 这是
// 真实发生过的故障。而「已登录时拿到的是哪种值」由 /api/v1/auth/session 决定，客户端无从选择。
//
// 因此按**本次请求有没有有效会话**分档，弱的那档只留给匿名：
//
//   - 有有效会话（会话中间件的判定结果，见 Manager.Middleware）→ 请求值必须等于会话行里的
//     CSRF token。这一档比双提交更强：攻击者即使能往受害者浏览器写 cookie 也读不到会话行里的值。
//   - 匿名 → 双提交：csrf_double cookie 必须等于请求携带的镜像值。这是唯一不需要服务端状态的
//     做法，代价是攻击者若能给目标域写 cookie 即可绕过（故只用于匿名场景）。
//
// 注意**不要**把本中间件挂到登录后的端点：那些端点必须要求会话存在（无会话一律 403 csrf_no_session），
// 否则匿名请求可以只靠 cookie 对通过校验、再由 handler 以「无用户」状态继续。
func PreSessionCSRFMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		if sess, ok := CurrentSession(c); ok {
			submitted := submittedCSRFToken(c)
			if !validDoubleSubmitToken(submitted) ||
				subtle.ConstantTimeCompare([]byte(submitted), []byte(sess.CSRFToken)) != 1 {
				abortCSRF(c)
				return
			}
			c.Next()
			return
		}
		cookie, err := c.Cookie(CSRFDoubleSubmitCookieName)
		if err != nil || !validDoubleSubmitToken(cookie) ||
			subtle.ConstantTimeCompare([]byte(cookie), []byte(submittedCSRFToken(c))) != 1 {
			abortCSRF(c)
			return
		}
		c.Next()
	}
}

// submittedCSRFToken 取请求携带的镜像 token：优先请求头（SPA 的 JSON 请求），
// 退回表单字段（无脚本表单）。PostForm 会解析并缓存表单体，不会与后续 handler 的读取冲突。
func submittedCSRFToken(c *gin.Context) string {
	if token := c.GetHeader(CSRFHeaderName); token != "" {
		return token
	}
	return c.PostForm(CSRFFieldName)
}

// abortCSRF 以统一形态拒绝校验失败的写请求（稳定 code + 英文文案，前端按 code 映射提示）。
func abortCSRF(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
		"error": gin.H{"code": "csrf_failed", "message": "invalid or missing CSRF token"},
	})
}
