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
	// TOTP 二次验证（M1-16）：登录第二步与设置页路由集中在 internal/web/totp.go。
	s.registerTOTPRoutes(router)

	// SPA / 同源 JSON 会话与认证端点（DESIGN.md §4.3、§8.3）
	router.GET("/api/v1/auth/session", s.apiSession)
	router.GET("/api/v1/session", s.apiSession)
	router.POST("/api/v1/auth/login", auth.DoubleSubmitMiddleware(), s.apiLogin)
	router.POST("/api/v1/auth/logout", s.sessions.CSRFMiddleware(), s.apiLogout)
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

// registrationDenialKey 把策略或邀请的拒绝原因翻译成语言包 key（M1-6、M1-7）。
func registrationDenialKey(err error) string {
	switch {
	case errors.Is(err, auth.ErrEmailDomainNotAllowed):
		return "auth.error.email_domain_not_allowed"
	case errors.Is(err, auth.ErrInviteRequired):
		return "auth.error.invite_required"
	case errors.Is(err, store.ErrInviteNotFound), errors.Is(err, store.ErrInviteUsed),
		errors.Is(err, store.ErrInviteExpired), errors.Is(err, store.ErrInviteEmailMismatch):
		return "auth.error.invite_invalid"
	default:
		return "auth.error.registration_closed"
	}
}

// registerSubmit 按注册策略创建本地账号（M1-6、M1-7）。
//
// 放行分支，优先级从高到低：
//  1. 尚无活跃管理员 —— 首个管理员引导，closed 策略下唯一的合法入口（DESIGN.md §4.1）；
//  2. 携带有效邀请 token —— 一次性、可限定邮箱、可设过期；角色取自邀请；
//  3. 否则读 settings 里的注册策略与邮箱域名白名单判定（open / invite / closed）。
//
// 邀请接受是单一事务（B-12）：占用 token 与建号在同一事务里提交或回滚，
// 外界看不到“已使用→又变可用”的中间态；条件更新保证并发下一码一用。
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
	// 匿名入口限流（DESIGN.md §4.3）：与 /forgot-password 共用同一套 IP + 目标邮箱双维度固定
	// 窗口计数（各 5 次 / 15 分钟），任一超限即拒。放在表单校验与任何 DB 动作之前：被拒的请求
	// 不建号、不写审计、不发验证邮件；对外统一返回 429 + error.rate_limited。
	if s.anonRateLimited(c, email) {
		s.renderRegister(c, loc, http.StatusTooManyRequests, inviteToken, loc.T("error.rate_limited"))
		return
	}
	if msg := validateRegisterInput(loc, username, email, password); msg != "" {
		s.renderRegister(c, loc, http.StatusBadRequest, inviteToken, msg)
		return
	}

	now := time.Now().UTC()
	admins, err := s.users.CountActiveAdmins(ctx)
	if err != nil {
		s.logger.Error("count active admins", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	role := store.RoleUser
	var invite *store.Invite
	switch {
	case admins == 0:
		// 引导路径：无管理员时该账号成为管理员。
		role = store.RoleAdmin
	case inviteToken != "":
		if s.invites == nil {
			s.renderRegister(c, loc, http.StatusForbidden, inviteToken, loc.T("auth.error.invite_invalid"))
			return
		}
		inv, verr := s.invites.Validate(ctx, inviteToken, email, now)
		if verr != nil {
			s.logger.Info("invite rejected", "error", verr)
			s.renderRegister(c, loc, http.StatusForbidden, inviteToken, loc.T(registrationDenialKey(verr)))
			return
		}
		invite = inv
		if inv.Role != "" {
			role = inv.Role
		}
	default:
		settings, err := store.LoadSettings(ctx, s.db)
		if err != nil {
			s.logger.Error("load settings", "error", err)
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		policy := auth.ParseRegistrationPolicy(settings[auth.SettingKeyRegistrationPolicy])
		allowlist := auth.ParseEmailAllowlist(settings[auth.SettingKeyEmailAllowlist])
		if derr := auth.DecideRegistration(policy, email, allowlist); derr != nil {
			s.renderRegister(c, loc, http.StatusForbidden, inviteToken, loc.T(registrationDenialKey(derr)))
			return
		}
	}

	input := auth.CreateUserInput{
		Username:    username,
		Email:       email,
		DisplayName: display,
		Password:    password,
		Role:        role,
		Locale:      loc.Locale(),
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
				s.renderRegister(c, loc, http.StatusForbidden, inviteToken, loc.T("auth.error.invite_invalid"))
				return
			}
			s.logger.Error("create local user failed", "username", username, "error", aerr)
			s.renderRegister(c, loc, http.StatusConflict, inviteToken, loc.T("auth.error.create_failed"))
			return
		}
		u = created
	} else {
		created, cerr := s.accounts.CreateLocalUser(ctx, input)
		if cerr != nil {
			s.logger.Error("create local user failed", "username", username, "error", cerr)
			s.renderRegister(c, loc, http.StatusConflict, inviteToken, loc.T("auth.error.create_failed"))
			return
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
	// M1-19：注册后发一封邮箱验证邮件；SMTP 未配置时不发也不报错（用户可稍后在设置页重发）。
	s.sendEmailVerification(c, u)
	c.Redirect(http.StatusSeeOther, "/login")
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
	if msg := validateRegisterInput(loc, username, email, password); msg != "" {
		s.renderSetup(c, loc, http.StatusBadRequest, msg)
		return
	}
	u, err := s.accounts.CreateLocalUser(ctx, auth.CreateUserInput{
		Username:    username,
		Email:       email,
		DisplayName: display,
		Password:    password,
		Role:        store.RoleAdmin,
		Locale:      loc.Locale(),
	})
	if err != nil {
		s.logger.Error("create bootstrap admin failed", "username", username, "error", err)
		s.renderSetup(c, loc, http.StatusConflict, loc.T("auth.error.create_failed"))
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		Action:     store.ActionUserCreate,
		TargetType: "user",
		TargetID:   store.Ptr(u.ID),
		Detail:     map[string]any{"username": u.Username, "email": u.Email, "role": u.Role, "bootstrap": true},
	})
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

// validateRegisterInput 做表单级校验并返回已本地化的提示；全部通过时返回空串。
// 密码强度复用 internal/auth 的策略，不在这里定义第二套规则（DESIGN.md §4.3）。
func validateRegisterInput(loc *i18n.Localizer, username, email, password string) string {
	if username == "" {
		return loc.T("auth.error.username_required")
	}
	if email == "" {
		return loc.T("auth.error.email_required")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return loc.T("auth.error.email_invalid")
	}
	if password == "" {
		return loc.T("auth.error.password_required")
	}
	if len(password) < auth.MinPasswordLength {
		return loc.T("auth.error.password_too_short")
	}
	if len(password) > auth.MaxPasswordLength {
		return loc.T("auth.error.password_too_long")
	}
	if err := auth.ValidatePasswordPolicy(password); err != nil {
		return loc.T("auth.error.password_too_common")
	}
	return ""
}
