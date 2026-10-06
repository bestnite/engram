package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// apiLoginRequest 是 /api/v1/auth/login 的请求体（支持 JSON 与表单输入）。
type apiLoginRequest struct {
	Username string `json:"username" form:"username"`
	Password string `json:"password" form:"password"`
}

// apiSession 提供同源 SPA 会话引导与 CSRF token 读取（DESIGN.md §4.3、§8.3、§11）。
//
// 无论登录与否，均不暴露 session ID、签名密钥或任何内部凭据；
// 已登录时返回当前用户信息与绑定在服务端会话上的 CSRF token；
// 未登录或用户被禁用时返回 authenticated=false、user=null，并下发/镜像双提交 cookie 对应的 CSRF token，
// 供 SPA 执行同源登录等会话前请求使用。
func (s *Server) apiSession(c *gin.Context) {
	u, okUser := auth.CurrentUser(c)
	sess, okSess := auth.CurrentSession(c)
	if okUser && okSess && u.Status == store.StatusActive {
		c.JSON(http.StatusOK, gin.H{
			"authenticated": true,
			"user": gin.H{
				"id":           u.ID,
				"username":     u.Username,
				"email":        u.Email,
				"display_name": u.DisplayName,
				"role":         u.Role,
				"locale":       u.Locale,
			},
			"csrf_token": sess.CSRFToken,
		})
		return
	}

	// 未登录或账号非 active：下发/复用双提交 CSRF token，cookie 置为 HttpOnly
	csrfToken := auth.EnsureDoubleSubmitToken(c, s.secureCookies())
	c.JSON(http.StatusOK, gin.H{
		"authenticated": false,
		"user":          nil,
		"csrf_token":    csrfToken,
	})
}

// apiLogin 校验凭据并建立服务端会话（DESIGN.md §4.3、§11）。
//
// 挂载 auth.DoubleSubmitMiddleware 保证请求同时携带 csrf_double cookie 与 X-CSRF-Token 头。
// 密码校验、限流递增延迟、恒定耗时 dummy hash、审计日志与 TOTP 二次验证均复用既有服务语义。
func (s *Server) apiLogin(c *gin.Context) {
	var req apiLoginRequest
	if err := c.ShouldBind(&req); err != nil && c.Request.ContentLength > 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    api.CodeInvalidRequest,
				"message": "The request is invalid.",
			},
		})
		return
	}

	username := strings.TrimSpace(req.Username)
	if username == "" {
		username = strings.TrimSpace(c.PostForm("username"))
	}
	password := req.Password
	if password == "" {
		password = c.PostForm("password")
	}

	ctx := c.Request.Context()
	ip := c.ClientIP()

	// 认证前先按已累计的失败次数递增延迟（M1-9）：防爆破，拉平暴力尝试速率
	if s.loginLimiter != nil {
		if _, err := s.loginLimiter.Wait(ctx, username, ip); err != nil {
			s.logger.Info("login delay aborted", "error", err)
			c.AbortWithStatus(http.StatusRequestTimeout)
			return
		}
	}

	u, err := s.accounts.Authenticate(ctx, username, password)
	if err != nil {
		if s.loginLimiter != nil {
			s.loginLimiter.RecordFailure(username, ip)
		}
		s.logger.Info("login failed", "username", username, "error", err)
		s.audit(ctx, store.AuditEntry{
			Action: store.ActionUserLoginFailed,
			Detail: map[string]any{"username": username, "ip": ip, "reason": err.Error()},
		})
		code := api.CodeInvalidCredentials
		msg := "Invalid username or password."
		if errors.Is(err, auth.ErrUserDisabled) {
			code = api.CodeUserDisabled
			msg = "This account has been disabled."
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{
				"code":    code,
				"message": msg,
			},
		})
		return
	}

	// TOTP 账号：第一因素通过后下发短期 pending cookie，并返回 TOTP challenge，绝不提早发放会话
	if s.totp != nil {
		enabled, err := s.totp.Enabled(ctx, u.ID)
		if err != nil {
			s.logger.Error("totp enabled check failed", "user_id", u.ID, "error", err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": gin.H{
					"code":    api.CodeInternal,
					"message": "An internal error occurred.",
				},
			})
			return
		}
		if enabled {
			s.setTOTPPendingCookie(c, u.ID)
			c.JSON(http.StatusOK, gin.H{
				"requires_totp": true,
			})
			return
		}
	}

	if s.loginLimiter != nil {
		s.loginLimiter.Reset(username, ip)
	}

	sess, err := s.sessions.StartSession(ctx, c, u.ID)
	if err != nil {
		s.logger.Error("start session failed", "user_id", u.ID, "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    api.CodeInternal,
				"message": "An internal error occurred.",
			},
		})
		return
	}

	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID),
		Action: store.ActionUserLoginSucceeded,
		Detail: map[string]any{"ip": ip},
	})
	s.notifyNewDeviceLogin(c, u)

	c.JSON(http.StatusOK, gin.H{
		"authenticated": true,
		"user": gin.H{
			"id":           u.ID,
			"username":     u.Username,
			"email":        u.Email,
			"display_name": u.DisplayName,
			"role":         u.Role,
			"locale":       u.Locale,
		},
		"csrf_token": sess.CSRFToken,
	})
}

// spaLoginShell 是 SPA 登录入口（GET /spa/login）。
//
// 与 SSR 的 GET /login 并存，不遮蔽它：后者仍是当前被链接、且无脚本也能提交的登录页，
// SPA 在浏览器端到端验证前走独立的 /spa 迁移目标路径（与 /spa/review、/spa/settings/totp
// 同一约定，DESIGN.md §8.1）。
//
// 像 SSR 的 GET /login 一样先初始化会话前双提交 cookie：SPA 挂载后从
// GET /api/v1/auth/session 取回同一 token 放进 X-CSRF-Token，POST /api/v1/auth/login
// 的 DoubleSubmitMiddleware 据此比对 cookie 与镜像值。这里只下发 cookie 并返回应用壳，
// 不渲染表单、不建立会话、不返回任何凭据（会话 cookie 始终由服务端在登录成功后签发）。
func (s *Server) spaLoginShell(c *gin.Context) {
	auth.EnsureDoubleSubmitToken(c, s.secureCookies())
	s.spa.ServeIndex(c)
}

// apiLogout 作废当前会话并清除 cookie（DESIGN.md §4.3、§11）。
//
// 挂载 s.sessions.CSRFMiddleware 强制要求会话绑定的 CSRF token。
// 登出后重置双提交 CSRF cookie 并返回相应 token，保证后续无缝进入登录等流程。
func (s *Server) apiLogout(c *gin.Context) {
	ctx := c.Request.Context()
	var userID *uint64
	if u, ok := auth.CurrentUser(c); ok {
		userID = store.Ptr(u.ID)
	}
	if err := s.sessions.Logout(ctx, c); err != nil {
		s.logger.Error("logout failed", "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    api.CodeInternal,
				"message": "An internal error occurred.",
			},
		})
		return
	}
	s.audit(ctx, store.AuditEntry{UserID: userID, Action: store.ActionUserLogout})
	doubleToken := auth.EnsureDoubleSubmitToken(c, s.secureCookies())
	c.JSON(http.StatusOK, gin.H{
		"authenticated": false,
		"csrf_token":    doubleToken,
	})
}
