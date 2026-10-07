package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
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

// spaLoginShell 提供 GET /login：SPA 已加载时返回应用壳（DESIGN.md §8.1 的规范路径），
// 由客户端路由渲染登录页；登录协议走 GET /api/v1/auth/session + POST /api/v1/auth/login，
// 第二步走 GET /login/totp / POST /api/v1/auth/totp。
//
// 像 SSR 的登录页一样先初始化会话前双提交 cookie：SPA 挂载后从
// GET /api/v1/auth/session 取回同一 token 放进 X-CSRF-Token，POST /api/v1/auth/login
// 的 DoubleSubmitMiddleware 据此比对 cookie 与镜像值。这里只下发 cookie 并返回应用壳，
// 不渲染表单、不建立会话、不返回任何凭据（会话 cookie 始终由服务端在登录成功后签发）。
//
// POST /login 仍由 SSR 表单处理器承担（无脚本客户端仍可登录），SPA 未嵌入（降级构建）时
// GET 也回退 SSR 登录页。迁移期别名 /spa/login 由同一处理器服务。
func (s *Server) spaLoginShell(c *gin.Context) {
	if s.spa == nil {
		s.loginPage(c)
		return
	}
	auth.EnsureDoubleSubmitToken(c, s.secureCookies())
	s.spa.ServeIndex(c)
}

// spaRegisterShell 提供 GET /register：SPA 已加载时返回应用壳（DESIGN.md §8.1 的规范路径），
// 注册协议走 POST /api/v1/auth/register（DoubleSubmitMiddleware 据 cookie 与镜像 token 比对）。
// ?invite=<token> 由前端从 URL 读取并回填到请求体。这里只下发 cookie 并返回应用壳，
// 不建号、不建立会话、不返回任何凭据。
//
// POST /register 仍由 SSR 表单处理器承担，SPA 未嵌入（降级构建）时 GET 回退 SSR 注册页。
// 迁移期别名 /spa/register 由同一处理器服务。
func (s *Server) spaRegisterShell(c *gin.Context) {
	if s.spa == nil {
		s.registerPage(c)
		return
	}
	auth.EnsureDoubleSubmitToken(c, s.secureCookies())
	s.spa.ServeIndex(c)
}

// spaSetupShell 提供 GET /setup：SPA 已加载时返回应用壳（DESIGN.md §8.1 的规范路径），
// 引导协议走 POST /api/v1/auth/setup。可达性与 SSR 的 GET /setup 完全一致：已存在活跃管理员
// 时返回 404（一次性管理员门，避免被当作后门反复访问）。可达时下发会话前双提交 cookie 并
// 返回应用壳；这里只下发 cookie 并返回应用壳，不建号、不建会话。
//
// POST /setup 仍由 SSR 表单处理器承担，SPA 未嵌入（降级构建）时 GET 回退 SSR 引导页。
// 迁移期别名 /spa/setup 由同一处理器服务。
func (s *Server) spaSetupShell(c *gin.Context) {
	if !s.setupAvailable(c) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if s.spa == nil {
		s.setupPage(c)
		return
	}
	auth.EnsureDoubleSubmitToken(c, s.secureCookies())
	s.spa.ServeIndex(c)
}

// apiRegisterRequest 是 POST /api/v1/auth/register 的请求体。
// Invite 可为空：为空时按注册策略判定；非空时走邀请接受路径。
type apiRegisterRequest struct {
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
	Invite      string `json:"invite"`
}

// apiSetupRequest 是 POST /api/v1/auth/setup 的请求体。
// Email 可为空：为空且配置了 BOOTSTRAP_ADMIN_EMAIL 时采用环境变量兜底（与 SSR 一致）。
type apiSetupRequest struct {
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

// apiRegister 是 SPA 自助注册端点（POST /api/v1/auth/register）。
//
// 挂载 auth.DoubleSubmitMiddleware：请求必须同时携带 csrf_double cookie 与 X-CSRF-Token 头。
// 全部判定（匿名限流、校验、首个管理员引导、邀请事务、注册策略/白名单）复用
// attemptRegistration，与 SSR 的 registerSubmit 是同一份逻辑，因此策略语义不可能漂移。
// 成功不建立会话（与 SSR 注册后跳转登录一致），只返回 created=true。
func (s *Server) apiRegister(c *gin.Context) {
	var req apiRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiAuthError(c, http.StatusBadRequest, api.CodeInvalidRequest, "The request is invalid.")
		return
	}
	ctx := c.Request.Context()
	outcome := s.attemptRegistration(ctx, c.ClientIP(),
		strings.TrimSpace(req.Username),
		strings.ToLower(strings.TrimSpace(req.Email)),
		strings.TrimSpace(req.DisplayName),
		req.Password,
		strings.TrimSpace(req.Invite),
		s.requestLocale(c))
	if outcome.Code != "" {
		s.writeRegistrationError(c, outcome)
		return
	}
	// M1-19：注册后发一封邮箱验证邮件；SMTP 未配置时不发也不报错（与 SSR 一致）。
	s.sendEmailVerification(c, outcome.User)
	c.JSON(http.StatusOK, gin.H{"created": true})
}

// apiSetup 是 SPA 首个管理员引导端点（POST /api/v1/auth/setup）。
//
// 与 SSR 的 setupSubmit 同一可达性规则：没有活跃管理员时才可达，否则 404（一次性管理员门）。
// 建号与审计复用 attemptSetup，挂载 DoubleSubmitMiddleware。成功不建立会话。
func (s *Server) apiSetup(c *gin.Context) {
	if !s.setupAvailable(c) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	var req apiSetupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiAuthError(c, http.StatusBadRequest, api.CodeInvalidRequest, "The request is invalid.")
		return
	}
	ctx := c.Request.Context()
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" && s.bootstrapEmail != "" {
		// 容器化部署用 BOOTSTRAP_ADMIN_EMAIL 兜底：请求未填邮箱时采用环境变量值。
		email = strings.ToLower(strings.TrimSpace(s.bootstrapEmail))
	}
	outcome := s.attemptSetup(ctx,
		strings.TrimSpace(req.Username), email,
		strings.TrimSpace(req.DisplayName), req.Password, s.requestLocale(c))
	if outcome.Code != "" {
		apiAuthError(c, outcome.Status, outcome.Code, registrationErrorMessage(outcome.Code))
		return
	}
	c.JSON(http.StatusOK, gin.H{"created": true})
}

// writeRegistrationError 把注册失败结果写成 JSON 错误包壳。
// rate_limited 补 Retry-After；internal_error 归一到 api.CodeInternal；其余按 outcome.Status。
func (s *Server) writeRegistrationError(c *gin.Context, outcome registrationOutcome) {
	if outcome.Code == "rate_limited" {
		c.Header("Retry-After", strconv.Itoa(int(auth.DefaultAnonRateWindow.Seconds())))
	}
	if outcome.Code == "internal_error" {
		apiAuthError(c, http.StatusInternalServerError, api.CodeInternal, "An internal error occurred.")
		return
	}
	apiAuthError(c, outcome.Status, outcome.Code, registrationErrorMessage(outcome.Code))
}

// registrationErrorMessage 返回注册失败 code 对应的稳定英文兜底文案。
// 前端按 code 映射本地化提示，绝不解析这条 message（DESIGN.md §8.3）。
func registrationErrorMessage(code string) string {
	switch code {
	case "username_required":
		return "Enter a username."
	case "email_required":
		return "Enter an email address."
	case "email_invalid":
		return "Enter a valid email address."
	case "password_required":
		return "Enter a password."
	case "password_too_short":
		return "The password is too short."
	case "password_too_long":
		return "The password is too long."
	case "password_too_common":
		return "The password is too common."
	case "email_domain_not_allowed":
		return "This email domain is not allowed to register."
	case "invite_required":
		return "Registration requires an invite link."
	case "invite_invalid":
		return "The invite link is invalid."
	case "registration_closed":
		return "Self-service registration is closed."
	case "create_failed":
		return "The account could not be created."
	default:
		return "The request is invalid."
	}
}

// requestLocale 返回请求解析出的界面语言码，供新建用户写入 locale；本地化器缺失时返回空串。
func (s *Server) requestLocale(c *gin.Context) string {
	if loc := i18n.FromContext(c.Request.Context()); loc != nil {
		return loc.Locale()
	}
	return ""
}

// apiAuthError 写出认证类 SPA 接口的错误包壳（与 spaTOTPError 同形）。
// code 稳定且英文，message 为英文兜底文案，前端按 code 映射本地化提示。
func apiAuthError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
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
