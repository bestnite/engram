package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是 M1-16 的 Web 层：登录第二步的凭据 cookie，以及两步验证管理页的应用壳入口。
//
// 与共享热点隔离：路由与 handler 全部落在本文件，对 internal/web/auth.go 只做路由注册插入，
// 不重排既有代码。登录第二步与设置的读写协议在同源 JSON 端点上（totp_login.go / totp_api.go）。

const (
	// totpPendingCookieName 是「密码已通过、等待第二因素」的短期凭据 cookie 名。
	totpPendingCookieName = "engram_totp_pending"
	// totpPendingTTL 是第二步凭据的有效期：密码已验证，窗口必须短。
	totpPendingTTL = 5 * time.Minute
)

// registerTOTPRoutes 挂载 TOTP 的全部路由（M1-16）。
// 依赖未装配时跳过，保证 M0 阶段的测试仍能构造 Server。
func (s *Server) registerTOTPRoutes(router *gin.Engine) {
	if s.totp == nil || s.sessions == nil || s.users == nil || s.accounts == nil {
		return
	}
	// SPA 登录第二步的同源 JSON 协议：GET 报告是否持有有效的第二步
	// 凭据，POST 提交验证码并签发会话。挂双提交 cookie 中间件（登录前流程没有会话可绑 token）。
	router.GET("/api/v1/auth/totp", s.apiTOTPPending)
	router.POST("/api/v1/auth/totp", auth.PreSessionCSRFMiddleware(), s.apiTOTPSubmit)
	// 两步验证管理页只返回应用壳；读写走 /api/v1/settings/totp*（totp_api.go）。
	router.GET("/settings/totp", s.totpSettingsRoute)
}

// setTOTPPendingCookie 下发第二步凭据：内容对客户端不可读（HttpOnly + HMAC 签名）。
// 不在这里记录任何「账号已启用 TOTP」的标记；cookie 里只有 user_id 与过期时间。
func (s *Server) setTOTPPendingCookie(c *gin.Context, userID uint64) {
	exp := time.Now().Add(totpPendingTTL).Unix()
	payload := fmt.Sprintf("%d.%d", userID, exp)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     totpPendingCookieName,
		Value:    s.sessions.SignValue(payload),
		Path:     "/",
		MaxAge:   int(totpPendingTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.secureCookies(),
		SameSite: http.SameSiteLaxMode,
	})
}

// clearTOTPPendingCookie 立即过期第二步凭据（登录完成或凭据失效时）。
func (s *Server) clearTOTPPendingCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     totpPendingCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secureCookies(),
		SameSite: http.SameSiteLaxMode,
	})
}

// pendingTOTPUser 校验第二步凭据并返回用户 ID；签名不对、格式不对或已过期都返回 false。
func (s *Server) pendingTOTPUser(c *gin.Context) (uint64, bool) {
	cookie, err := c.Request.Cookie(totpPendingCookieName)
	if err != nil {
		return 0, false
	}
	payload, ok := s.sessions.VerifyValue(cookie.Value)
	if !ok {
		return 0, false
	}
	idPart, expPart, ok := strings.Cut(payload, ".")
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseUint(idPart, 10, 64)
	if err != nil || id == 0 {
		return 0, false
	}
	exp, err := strconv.ParseInt(expPart, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return 0, false
	}
	return id, true
}

// verifyUserPassword 校验当前用户的本地密码；纯 OIDC 账号（无密码）一律拒绝。
func verifyUserPassword(u *store.User, password string) error {
	if u.PasswordHash == nil {
		return auth.ErrInvalidCredentials
	}
	ok, err := auth.Verify(*u.PasswordHash, password)
	if err != nil {
		return err
	}
	if !ok {
		return auth.ErrInvalidCredentials
	}
	return nil
}

// totpSettingsRoute 提供 GET /settings/totp：返回应用壳，
// 由客户端路由渲染两步验证管理页；读写走 /api/v1/settings/totp*（同一份服务逻辑与审计），
// 因此页面迁移不新增任何写路径。授权判定与迁移前一致：未登录一律重定向登录页。
func (s *Server) totpSettingsRoute(c *gin.Context) {
	if _, ok := s.requireUser(c); !ok {
		return
	}
	s.shell.ServeIndex(c)
}
