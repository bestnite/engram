package web

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
)

// registerAuthRoutes 挂载认证路由（M1-4、M1-5）。
//
// 依赖未装配时（例如 M0 阶段的测试只构造了 DB/Logger）直接跳过，保证 New 仍可用；
// 生产装配见 cmd/engram，需要显式提供 Accounts、Sessions、Users。
func (s *Server) registerAuthRoutes(router *gin.Engine) {
	if s.accounts == nil || s.sessions == nil || s.users == nil {
		return
	}
	// 登录/注册/引导是登录前流程：此时还没有服务端会话，会话绑定的 CSRF token 无从产生。
	// 这三条 POST 改用双提交 cookie（B-13）：GET 下发随机 token 的 cookie 并镜像进表单，
	// 提交时中间件比对两者，缺镜像 cookie 一律 403。已有会话的写请求仍走会话绑定的 CSRF。
	router.GET("/login", s.loginPage)
	router.POST("/login", auth.DoubleSubmitMiddleware(), s.loginSubmit)
	router.GET("/register", s.registerPage)
	router.POST("/register", auth.DoubleSubmitMiddleware(), s.registerSubmit)
	router.GET("/setup", s.setupPage)
	router.POST("/setup", auth.DoubleSubmitMiddleware(), s.setupSubmit)
	// 登出是登录后流程：会话已存在，必须携带会话绑定的 CSRF token（DESIGN.md §4.3）。
	router.POST("/logout", s.sessions.CSRFMiddleware(), s.logout)
	// OIDC 可选登录（M1-11）：默认关闭，配置不完整时 handler 返回 404（不允许半开）。
	// 与 /login 同属登录前流程，没有会话可绑 CSRF token，故不挂 CSRFMiddleware。
	router.GET("/auth/oidc/start", s.oidcStart)
	router.GET("/auth/oidc/callback", s.oidcCallback)
	// SPA 的 OIDC 入口探测（spa_oidc.go）：只读，供登录视图决定是否显示第二个登录按钮。
	s.registerSPAOIDCRoutes(router)
	// TOTP 二次验证（M1-16）：登录第二步与设置页路由集中在 internal/web/totp.go。
	s.registerTOTPRoutes(router)

	// SPA / 同源 JSON 会话与认证端点（DESIGN.md §4.3、§8.3）
	router.GET("/api/v1/auth/session", s.apiSession)
	router.GET("/api/v1/session", s.apiSession)
	router.POST("/api/v1/auth/login", auth.DoubleSubmitMiddleware(), s.apiLogin)
	// 注册与引导是登录前流程：同样没有服务端会话，POST 走双提交 cookie（与 SSR 的
	// /register、/setup 同一中间件）。判定与建号复用 SSR 的同一份服务逻辑。
	router.POST("/api/v1/auth/register", auth.DoubleSubmitMiddleware(), s.apiRegister)
	router.POST("/api/v1/auth/setup", auth.DoubleSubmitMiddleware(), s.apiSetup)
	router.POST("/api/v1/auth/logout", s.sessions.CSRFMiddleware(), s.apiLogout)

	// SPA 登录/注册/引导入口（迁移目标路径 /spa/*）：SSR 的 GET/POST /login、/register、/setup
	// 保持原样、不被遮蔽。只返回应用壳并初始化会话前双提交 cookie，协议仍是上面的 JSON 端点。
	if s.spa != nil {
		router.GET("/spa/login", s.spaLoginShell)
		router.GET("/spa/register", s.spaRegisterShell)
		router.GET("/spa/setup", s.spaSetupShell)
		// 登录第二步（TOTP）的 SPA 入口。SSR 只注册 POST /login/totp，没有可遮蔽的 GET 页面；
		// 仍按迁移期约定走 /spa 前缀。协议是 GET/POST /api/v1/auth/totp。
		router.GET("/spa/login/totp", s.spaTOTPLoginShell)
	}
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

// authLayout 构造认证页外壳数据；标题文案由调用方给定语言包 key。
// 页头的语言切换下拉与页脚仓库链接由 decorateLayout 统一补齐。
func (s *Server) authLayout(c *gin.Context, loc *i18n.Localizer, titleKey string) views.LayoutData {
	layout := views.LayoutData{
		Lang:       loc.Locale(),
		Title:      loc.T(titleKey),
		Brand:      loc.T("app.name"),
		HomeURL:    "/",
		CSSURL:     s.assets.URL("css/tailwind.css"),
		HTMXURL:    s.assets.URL("js/htmx.min.js"),
		MathJaxURL: s.assets.URL("js/mathjax/tex-svg.js"),
	}
	s.decorateLayout(c, loc, &layout)
	return layout
}

// renderAuth 写出认证页；status 用于把校验失败渲染成 4xx 而不是一律 200。
func (s *Server) renderAuth(c *gin.Context, status int, data views.AuthFormData) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(status)
	if err := views.AuthPage(data).Render(c.Request.Context(), c.Writer); err != nil {
		s.logger.Error("render template failed", "error", err, "path", c.Request.URL.Path)
	}
}

// loginPage 渲染登录表单。
func (s *Server) loginPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	s.renderLogin(c, loc, http.StatusOK, "")
}

// renderLogin 渲染登录表单并带上一条已本地化的错误提示（可为空）。
func (s *Server) renderLogin(c *gin.Context, loc *i18n.Localizer, status int, errMsg string) {
	data := views.AuthFormData{
		Layout:               s.authLayout(c, loc, "auth.login.title"),
		Heading:              loc.T("auth.login.heading"),
		Action:               "/login",
		SubmitLabel:          loc.T("auth.login.submit"),
		ErrorMessage:         errMsg,
		CSRF:                 auth.EnsureDoubleSubmitToken(c, s.secureCookies()),
		UsernameLabel:        loc.T("auth.field.username"),
		PasswordLabel:        loc.T("auth.field.password"),
		PasswordAutocomplete: "current-password",
		AltLabel:             loc.T("auth.login.to_register"),
		AltHref:              "/register",
	}
	// OIDC 默认关闭：只有配置完整可用时登录页才出现第二个登录入口（DESIGN.md §4.4）。
	if cfg, err := s.oidcLoadConfig(c); err == nil && cfg.Usable() {
		data.OIDCEnabled = true
		data.OIDCLabel = loc.T("auth.oidc.button")
		data.OIDCHref = "/auth/oidc/start"
	}
	s.renderAuth(c, status, data)
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

// loginSubmit 校验凭据、建立服务端会话并重定向到首页；失败时回填错误提示而不是跳转。
func (s *Server) loginSubmit(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	username := strings.TrimSpace(c.PostForm("username"))
	ip := c.ClientIP()
	// 认证前先按已累计的失败次数递增延迟（M1-9）：防爆破，也拉平暴力尝试的速率。
	if s.loginLimiter != nil {
		if _, err := s.loginLimiter.Wait(ctx, username, ip); err != nil {
			s.logger.Info("login delay aborted", "error", err)
			c.AbortWithStatus(http.StatusRequestTimeout)
			return
		}
	}
	u, err := s.accounts.Authenticate(ctx, username, c.PostForm("password"))
	if err != nil {
		if s.loginLimiter != nil {
			s.loginLimiter.RecordFailure(username, ip)
		}
		// 只记用户名与错误，绝不记录密码（AGENTS.md §2.1：日志英文）。
		s.logger.Info("login failed", "username", username, "error", err)
		s.audit(ctx, store.AuditEntry{
			Action: store.ActionUserLoginFailed,
			Detail: map[string]any{"username": username, "ip": ip, "reason": err.Error()},
		})
		key := "auth.error.invalid_credentials"
		if errors.Is(err, auth.ErrUserDisabled) {
			key = "auth.error.user_disabled"
		}
		s.renderLogin(c, loc, http.StatusUnauthorized, loc.T(key))
		return
	}
	// M1-16：启用了 TOTP 的账号，密码只是第一因素；这里不建立会话，改为进入第二步。
	// 放在限速清零之前：只有第二因素也通过才算本次登录成功。
	if s.beginTOTPChallengeIfEnabled(c, loc, u) {
		return
	}
	// 成功后清零该账号与 IP 的失败计数（M1-9 验收点）。
	if s.loginLimiter != nil {
		s.loginLimiter.Reset(username, ip)
	}
	if _, err := s.sessions.StartSession(ctx, c, u.ID); err != nil {
		s.logger.Error("start session failed", "user_id", u.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID),
		Action: store.ActionUserLoginSucceeded,
		Detail: map[string]any{"ip": ip},
	})
	// M1-19：记录登录指纹，新设备/新 IP 时投递提醒；发信失败不影响登录。
	s.notifyNewDeviceLogin(c, u)
	c.Redirect(http.StatusSeeOther, "/")
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

// registerPage 渲染自助注册表单；?invite=<token> 时把邀请 token 带进表单（M1-7）。
func (s *Server) registerPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	s.renderRegister(c, loc, http.StatusOK, strings.TrimSpace(c.Query("invite")), "")
}

// renderRegister 渲染注册表单并带上一条已本地化的错误提示（可为空）。
// inviteToken 非空时表单 action 与隐藏字段都携带它，提交后接受路径据此放行。
func (s *Server) renderRegister(c *gin.Context, loc *i18n.Localizer, status int, inviteToken, errMsg string) {
	action := "/register"
	intro := ""
	if inviteToken != "" {
		action = "/register?invite=" + url.QueryEscape(inviteToken)
		intro = loc.T("auth.register.invite_intro")
	}
	s.renderAuth(c, status, views.AuthFormData{
		Layout:               s.authLayout(c, loc, "auth.register.title"),
		Heading:              loc.T("auth.register.heading"),
		Intro:                intro,
		Action:               action,
		SubmitLabel:          loc.T("auth.register.submit"),
		ErrorMessage:         errMsg,
		CSRF:                 auth.EnsureDoubleSubmitToken(c, s.secureCookies()),
		UsernameLabel:        loc.T("auth.field.username"),
		EmailLabel:           loc.T("auth.field.email"),
		PasswordLabel:        loc.T("auth.field.password"),
		DisplayNameLabel:     loc.T("auth.field.display_name"),
		ShowEmail:            true,
		ShowDisplayName:      true,
		PasswordAutocomplete: "new-password",
		AltLabel:             loc.T("auth.register.to_login"),
		AltHref:              "/login",
		InviteToken:          inviteToken,
	})
}

// registrationOutcome 是注册与引导判定的结果：Code 为空表示成功并携带新用户。
// Status 是 SSR 表单渲染用的 HTTP 状态；JSON 接口直接采用同一 status。
type registrationOutcome struct {
	Status int
	Code   string // 稳定英文 code；空串表示成功
	User   *store.User
}

// registrationDenialCode 把策略或邀请的拒绝原因翻译成稳定英文 code（M1-6、M1-7）。
// code 同时是语言包键 auth.error.<code> 的后缀与 JSON 错误包壳里的 code。
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

// registrationMessageKey 把注册失败的 code 映射成语言包键。
// rate_limited 与 auth.error.* 不在同一命名空间，单独映射；其余 code 直接拼 auth.error.。
func registrationMessageKey(code string) string {
	if code == "rate_limited" {
		return "error.rate_limited"
	}
	return "auth.error." + code
}

// attemptRegistration 执行自助注册的全部服务端判定：匿名限流、表单校验、首个管理员引导、
// 邀请接受（事务化）与注册策略/邮箱白名单。SSR 表单（registerSubmit）与 SPA JSON 接口
// （apiRegister）共用它，因此两条传输的策略、限流、审计与事务语义不可能漂移。
//
// 它不写响应、不发邮件：调用方据 outcome 渲染或映射。放行分支与拒绝顺序见 DESIGN.md §4.1、§4.2、§4.3。
func (s *Server) attemptRegistration(ctx context.Context, clientIP, username, email, displayName, password, inviteToken, locale string) registrationOutcome {
	// 匿名入口限流（DESIGN.md §4.3）：与 /forgot-password 共用同一套 IP + 目标邮箱双维度固定
	// 窗口计数（各 5 次 / 15 分钟），任一超限即拒。放在表单校验与任何 DB 动作之前：被拒的请求
	// 不建号、不写审计、不发验证邮件。
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

// attemptSetup 执行首个管理员引导的建号与审计（M1-5）。SSR 表单（setupSubmit）与
// SPA JSON 接口（apiSetup）共用它，一次性管理员门（CountActiveAdmins==0）由调用方在
// 入口处校验，与 SSR 的 setupAvailable 规则一致。引导不发送邮箱验证邮件。
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

// registerSubmit 按注册策略创建本地账号（M1-6、M1-7）。
//
// 放行与拒绝的判定全部委托给 attemptRegistration（与 SPA JSON 接口同一份逻辑），
// 这里只负责表单取值、把 code 映射成本地化提示并渲染，以及成功后发送邮箱验证邮件。
func (s *Server) registerSubmit(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	inviteToken := strings.TrimSpace(c.PostForm("invite"))
	if inviteToken == "" {
		inviteToken = strings.TrimSpace(c.Query("invite"))
	}
	username := strings.TrimSpace(c.PostForm("username"))
	email := strings.ToLower(strings.TrimSpace(c.PostForm("email")))
	display := strings.TrimSpace(c.PostForm("display_name"))
	password := c.PostForm("password")

	outcome := s.attemptRegistration(ctx, c.ClientIP(), username, email, display, password, inviteToken, loc.Locale())
	if outcome.Code != "" {
		s.renderRegistrationFailure(c, loc, outcome, inviteToken)
		return
	}
	// M1-19：注册后发一封邮箱验证邮件；SMTP 未配置时不发也不报错（用户可稍后在设置页重发）。
	s.sendEmailVerification(c, outcome.User)
	c.Redirect(http.StatusSeeOther, "/login")
}

// renderRegistrationFailure 把注册失败结果渲染成认证页。
// rate_limited 补 Retry-After；internal_error 只回 500（与既有 SSR 行为一致，不回显内部原因）；
// 其余按 outcome.Status 渲染本地化提示。
func (s *Server) renderRegistrationFailure(c *gin.Context, loc *i18n.Localizer, outcome registrationOutcome, inviteToken string) {
	if outcome.Code == "rate_limited" {
		c.Header("Retry-After", strconv.Itoa(int(auth.DefaultAnonRateWindow.Seconds())))
	}
	if outcome.Code == "internal_error" {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.renderRegister(c, loc, outcome.Status, inviteToken, loc.T(registrationMessageKey(outcome.Code)))
}

// setupPage 渲染首个管理员引导页；已存在管理员时返回 404（M1-5 验收点）。
func (s *Server) setupPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	if !s.setupAvailable(c) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	s.renderSetup(c, loc, http.StatusOK, "")
}

// setupAvailable 报告引导页是否可达：仅当没有任何仍在用的管理员时可达。
func (s *Server) setupAvailable(c *gin.Context) bool {
	n, err := s.users.CountActiveAdmins(c.Request.Context())
	if err != nil {
		s.logger.Error("count active admins", "error", err)
		return false
	}
	return n == 0
}

// renderSetup 渲染引导页；BOOTSTRAP_ADMIN_EMAIL 作为邮箱兜底预填（DESIGN.md §4.1）。
func (s *Server) renderSetup(c *gin.Context, loc *i18n.Localizer, status int, errMsg string) {
	s.renderAuth(c, status, views.AuthFormData{
		Layout:               s.authLayout(c, loc, "auth.setup.title"),
		Heading:              loc.T("auth.setup.heading"),
		Intro:                loc.T("auth.setup.intro"),
		Action:               "/setup",
		SubmitLabel:          loc.T("auth.setup.submit"),
		ErrorMessage:         errMsg,
		CSRF:                 auth.EnsureDoubleSubmitToken(c, s.secureCookies()),
		UsernameLabel:        loc.T("auth.field.username"),
		EmailLabel:           loc.T("auth.field.email"),
		PasswordLabel:        loc.T("auth.field.password"),
		DisplayNameLabel:     loc.T("auth.field.display_name"),
		ShowEmail:            true,
		ShowDisplayName:      true,
		EmailValue:           s.bootstrapEmail,
		PasswordAutocomplete: "new-password",
	})
}

// setupSubmit 创建首个管理员；引导页不可达时同样返回 404，避免被当作后门重复调用。
// 建号与审计委托给 attemptSetup（与 SPA JSON 接口同一份逻辑）。
func (s *Server) setupSubmit(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	if !s.setupAvailable(c) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	ctx := c.Request.Context()
	username := strings.TrimSpace(c.PostForm("username"))
	email := strings.ToLower(strings.TrimSpace(c.PostForm("email")))
	if email == "" && s.bootstrapEmail != "" {
		// 容器化部署用 BOOTSTRAP_ADMIN_EMAIL 兜底：表单未填邮箱时采用环境变量值。
		email = strings.ToLower(strings.TrimSpace(s.bootstrapEmail))
	}
	display := strings.TrimSpace(c.PostForm("display_name"))
	password := c.PostForm("password")

	outcome := s.attemptSetup(ctx, username, email, display, password, loc.Locale())
	if outcome.Code != "" {
		s.renderSetup(c, loc, outcome.Status, loc.T("auth.error."+outcome.Code))
		return
	}
	c.Redirect(http.StatusSeeOther, "/login")
}

// logout 作废当前会话并清除 cookie，然后回到登录页。
func (s *Server) logout(c *gin.Context) {
	ctx := c.Request.Context()
	var userID *uint64
	if u, ok := auth.CurrentUser(c); ok {
		userID = store.Ptr(u.ID)
	}
	if err := s.sessions.Logout(ctx, c); err != nil {
		s.logger.Error("logout failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.audit(ctx, store.AuditEntry{UserID: userID, Action: store.ActionUserLogout})
	c.Redirect(http.StatusSeeOther, "/login")
}

// registerInputErrorCode 校验注册与引导表单输入并返回稳定英文 code；通过时返回空串。
//
// SSR 表单（registerSubmit/setupSubmit）与 SPA JSON 接口（apiRegister/apiSetup）共用它，
// 避免两套传输各写一份校验而漂移（AGENTS.md §2.4「一个业务层，两种传输」）。
// code 的取值就是语言包键 auth.error.<code> 的后缀，也是 JSON 错误包壳里的稳定 code。
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
