package web

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/store"
)

// registerAuthRoutes 挂载认证路由（M1-4、M1-5）。
//
// 依赖未装配时（例如 M0 阶段的测试只构造了 DB/Logger）直接跳过，保证 New 仍可用；
// 生产装配见 cmd/engram，需要显式提供 Accounts、Sessions、Users。
func (s *Server) registerAuthRoutes(router *gin.Engine) {
	if s.accounts == nil || s.sessions == nil || s.users == nil {
		return
	}
	// 登录/注册/引导三条页面只返回 SPA 应用壳（DESIGN.md §8.1）：页面判定与写操作全部由
	// 客户端的同源 JSON 端点承担（/api/v1/auth/{session,login,register,setup}）。
	// 会话前写请求没有服务端会话可绑 token，仍由 PreSessionCSRFMiddleware 校验双提交 cookie
	// 与镜像 token（B-13）。
	router.GET("/login", s.spaLoginShell)
	router.GET("/register", s.spaRegisterShell)
	router.GET("/setup", s.spaSetupShell)
	// OIDC 可选登录（M1-11）：默认关闭，配置不完整时 handler 返回 404（不允许半开）。
	router.GET("/auth/oidc/start", s.oidcStart)
	router.GET("/auth/oidc/callback", s.oidcCallback)
	// SPA 的 OIDC 入口探测（spa_oidc.go）：只读，供登录视图决定是否显示第二个登录按钮。
	s.registerSPAOIDCRoutes(router)
	// TOTP 二次验证（M1-16）：登录第二步与设置页路由集中在 internal/web/totp.go。
	s.registerTOTPRoutes(router)

	// SPA / 同源 JSON 会话与认证端点（DESIGN.md §4.3、§8.3）
	router.GET("/api/v1/auth/session", s.apiSession)
	router.GET("/api/v1/session", s.apiSession)
	router.POST("/api/v1/auth/login", auth.PreSessionCSRFMiddleware(), s.apiLogin)
	// 注册与引导是登录前流程：同样没有服务端会话，POST 走双提交 cookie。
	router.POST("/api/v1/auth/register", auth.PreSessionCSRFMiddleware(), s.apiRegister)
	router.POST("/api/v1/auth/setup", auth.PreSessionCSRFMiddleware(), s.apiSetup)
	router.POST("/api/v1/auth/logout", s.sessions.CSRFMiddleware(), s.apiLogout)

	// 迁移期别名：/spa/login、/spa/register、/spa/setup、/spa/login/totp 与规范路径
	// 共用同一处理器（同一份双提交 cookie 与可达性判定），保留是为了既有深链不失效。
	router.GET("/spa/login", s.spaLoginShell)
	router.GET("/spa/register", s.spaRegisterShell)
	router.GET("/spa/setup", s.spaSetupShell)
	// 登录第二步（TOTP）的 SPA 入口。规范路径 /login/totp 由 NoRoute 回退到应用壳；
	// 协议是 GET/POST /api/v1/auth/totp。
	router.GET("/spa/login/totp", s.spaTOTPLoginShell)
}

// localizer 从请求 context 取本地化器；缺失属于装配缺陷，记英文日志并 500。
func (s *Server) localizer(c *gin.Context) (*i18n.Localizer, bool) {
	loc := i18n.FromContext(c.Request.Context())
	if loc == nil {
		s.logger.Error("i18n: localizer missing from request context", "path", c.Request.URL.Path)
		c.AbortWithStatus(http.StatusInternalServerError)
		return nil, false
	}
	return loc, true
}

// audit 是写审计的统一出口（M1-10）：所有变更都经这里落 audit_log。
// 审计写失败只记英文日志、不回滚已发生的业务变更 —— 但绝不静默，否则审计会悄悄缺行。
func (s *Server) audit(ctx context.Context, e store.AuditEntry) {
	if s.auditor == nil {
		return
	}
	if err := s.auditor.Record(ctx, e); err != nil {
		s.logger.Error("write audit log failed", "action", e.Action, "error", err)
	}
}

// anonRateLimited 报告一次匿名入口请求是否应被限流拒绝，并在拒绝时补 Retry-After 响应头。
// /register 与 /forgot-password 共用同一套 "IP + 目标邮箱 各 5 次 / 15 分钟" 的双维度计数
// （DESIGN.md §4.3）。未装配限流器时一律放行，与 loginLimiter 的装配约定一致。
func (s *Server) anonRateLimited(c *gin.Context, email string) bool {
	if s.anonLimiter == nil {
		return false
	}
	if s.anonLimiter.Allow(c.ClientIP(), email) {
		return false
	}
	c.Header("Retry-After", strconv.Itoa(int(auth.DefaultAnonRateWindow.Seconds())))
	return true
}

// registrationOutcome 是注册与引导判定的结果：Code 为空表示成功并携带新用户。
// Status 是 HTTP 状态；JSON 传输直接采用同一 status。
type registrationOutcome struct {
	Status int
	Code   string // 稳定英文 code；空串表示成功
	User   *store.User
}

// registrationDenialCode 把策略或邀请的拒绝原因翻译成稳定英文 code（M1-6、M1-7）。
// code 同时是 JSON 错误包壳里的 code 与稳定英文 message 的来源。
func registrationDenialCode(err error) string {
	switch {
	case errors.Is(err, auth.ErrEmailDomainNotAllowed):
		return "email_domain_not_allowed"
	case errors.Is(err, auth.ErrInviteRequired):
		return "invite_required"
	case errors.Is(err, store.ErrInviteNotFound), errors.Is(err, store.ErrInviteUsed),
		errors.Is(err, store.ErrInviteExpired), errors.Is(err, store.ErrInviteEmailMismatch):
		return "invite_invalid"
	default:
		return "registration_closed"
	}
}

// attemptRegistration 执行自助注册的全部服务端判定：匿名限流、表单校验、首个管理员引导、
// 邀请接受（事务化）与注册策略/邮箱白名单。SPA JSON 接口（apiRegister）是唯一的传输，
// 因此策略、限流、审计与事务语义只有一份实现。
//
// 它不写响应、不发邮件：调用方据 outcome 映射。放行分支与拒绝顺序见 DESIGN.md §4.1、§4.2、§4.3。
func (s *Server) attemptRegistration(ctx context.Context, clientIP, username, email, displayName, password, inviteToken, locale string) registrationOutcome {
	// 匿名入口限流（DESIGN.md §4.3）：与 /api/v1/auth/forgot-password 共用同一套 IP + 目标邮箱
	// 双维度固定窗口计数（各 5 次 / 15 分钟），任一超限即拒。放在表单校验与任何 DB 动作之前：
	// 被拒的请求不建号、不写审计、不发验证邮件。
	if s.anonLimiter != nil && !s.anonLimiter.Allow(clientIP, email) {
		return registrationOutcome{Status: http.StatusTooManyRequests, Code: "rate_limited"}
	}
	if code := registerInputErrorCode(username, email, password); code != "" {
		return registrationOutcome{Status: http.StatusBadRequest, Code: code}
	}

	now := time.Now().UTC()
	admins, err := s.users.CountActiveAdmins(ctx)
	if err != nil {
		s.logger.Error("count active admins", "error", err)
		return registrationOutcome{Status: http.StatusInternalServerError, Code: "internal_error"}
	}

	role := store.RoleUser
	var invite *store.Invite
	switch {
	case admins == 0:
		// 引导路径：无管理员时该账号成为管理员。
		role = store.RoleAdmin
	case inviteToken != "":
		if s.invites == nil {
			return registrationOutcome{Status: http.StatusForbidden, Code: "invite_invalid"}
		}
		inv, verr := s.invites.Validate(ctx, inviteToken, email, now)
		if verr != nil {
			s.logger.Info("invite rejected", "error", verr)
			return registrationOutcome{Status: http.StatusForbidden, Code: registrationDenialCode(verr)}
		}
		invite = inv
		if inv.Role != "" {
			role = inv.Role
		}
	default:
		settings, err := store.LoadSettings(ctx, s.db)
		if err != nil {
			s.logger.Error("load settings", "error", err)
			return registrationOutcome{Status: http.StatusInternalServerError, Code: "internal_error"}
		}
		policy := auth.ParseRegistrationPolicy(settings[auth.SettingKeyRegistrationPolicy])
		allowlist := auth.ParseEmailAllowlist(settings[auth.SettingKeyEmailAllowlist])
		if derr := auth.DecideRegistration(policy, email, allowlist); derr != nil {
			return registrationOutcome{Status: http.StatusForbidden, Code: registrationDenialCode(derr)}
		}
	}

	input := auth.CreateUserInput{
		Username:    username,
		Email:       email,
		DisplayName: displayName,
		Password:    password,
		Role:        role,
		Locale:      locale,
	}

	var u *store.User
	if invite != nil {
		// 邀请接受事务化（B-12）：Accept 在一个事务里占用 token、建号并回填 used_by。
		// 建号失败时整体回滚，token 保持可用；并发下条件更新保证只有一个请求能占用成功。
		created, aerr := s.invites.Accept(ctx, invite.Token, now, func(tx *gorm.DB) (*store.User, error) {
			return s.accounts.CreateLocalUserTx(ctx, tx, input)
		})
		if aerr != nil {
			if errors.Is(aerr, store.ErrInviteUsed) {
				return registrationOutcome{Status: http.StatusForbidden, Code: "invite_invalid"}
			}
			s.logger.Error("create local user failed", "username", username, "error", aerr)
			return registrationOutcome{Status: http.StatusConflict, Code: "create_failed"}
		}
		u = created
	} else {
		created, cerr := s.accounts.CreateLocalUser(ctx, input)
		if cerr != nil {
			s.logger.Error("create local user failed", "username", username, "error", cerr)
			return registrationOutcome{Status: http.StatusConflict, Code: "create_failed"}
		}
		u = created
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		Action:     store.ActionUserCreate,
		TargetType: "user",
		TargetID:   store.Ptr(u.ID),
		Detail:     map[string]any{"username": u.Username, "email": u.Email, "role": u.Role},
	})
	return registrationOutcome{Status: http.StatusOK, User: u}
}

// attemptSetup 执行首个管理员引导的建号与审计（M1-5）。SPA JSON 接口（apiSetup）是唯一的传输，
// 一次性管理员门（CountActiveAdmins==0）由调用方在入口处校验。引导不发送邮箱验证邮件。
func (s *Server) attemptSetup(ctx context.Context, username, email, displayName, password, locale string) registrationOutcome {
	if code := registerInputErrorCode(username, email, password); code != "" {
		return registrationOutcome{Status: http.StatusBadRequest, Code: code}
	}
	u, err := s.accounts.CreateLocalUser(ctx, auth.CreateUserInput{
		Username:    username,
		Email:       email,
		DisplayName: displayName,
		Password:    password,
		Role:        store.RoleAdmin,
		Locale:      locale,
	})
	if err != nil {
		s.logger.Error("create bootstrap admin failed", "username", username, "error", err)
		return registrationOutcome{Status: http.StatusConflict, Code: "create_failed"}
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		Action:     store.ActionUserCreate,
		TargetType: "user",
		TargetID:   store.Ptr(u.ID),
		Detail:     map[string]any{"username": u.Username, "email": u.Email, "role": u.Role, "bootstrap": true},
	})
	return registrationOutcome{Status: http.StatusOK, User: u}
}

// setupAvailable 报告引导页是否可达：仅当没有任何仍在用的管理员时可达（M1-5 的一次性管理员门）。
func (s *Server) setupAvailable(c *gin.Context) bool {
	n, err := s.users.CountActiveAdmins(c.Request.Context())
	if err != nil {
		s.logger.Error("count active admins", "error", err)
		return false
	}
	return n == 0
}

// registerInputErrorCode 校验注册与引导表单输入并返回稳定英文 code；通过时返回空串。
//
// SPA JSON 接口（apiRegister/apiSetup）与管理员建号表单共用它，避免两套传输各写一份校验而
// 漂移（AGENTS.md §2.4「一个业务层，两种传输」）。
// code 的取值就是 JSON 错误包壳里的稳定 code，也是英文兜底 message 的来源。
// 密码强度复用 internal/auth 的策略，不在这里定义第二套规则（DESIGN.md §4.3）。
func registerInputErrorCode(username, email, password string) string {
	if username == "" {
		return "username_required"
	}
	if email == "" {
		return "email_required"
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return "email_invalid"
	}
	if password == "" {
		return "password_required"
	}
	if len(password) < auth.MinPasswordLength {
		return "password_too_short"
	}
	if len(password) > auth.MaxPasswordLength {
		return "password_too_long"
	}
	if err := auth.ValidatePasswordPolicy(password); err != nil {
		return "password_too_common"
	}
	return ""
}

// validateRegisterInput 做表单级校验并返回已本地化的提示；全部通过时返回空串。
// 判定委托给 registerInputErrorCode，本地化只是把同一份 code 映射成语言包文案。
func validateRegisterInput(loc *i18n.Localizer, username, email, password string) string {
	if code := registerInputErrorCode(username, email, password); code != "" {
		return loc.T("auth.error." + code)
	}
	return ""
}
