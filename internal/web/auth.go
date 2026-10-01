package web

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"

	"github.com/gin-gonic/gin"

	"example.com/flashcard/internal/auth"
	"example.com/flashcard/internal/i18n"
	"example.com/flashcard/internal/store"
	"example.com/flashcard/internal/web/views"
)

// registerAuthRoutes 挂载认证路由（M1-4、M1-5）。
//
// 依赖未装配时（例如 M0 阶段的测试只构造了 DB/Logger）直接跳过，保证 New 仍可用；
// 生产装配见 cmd/flashcard，需要显式提供 Accounts、Sessions、Users。
func (s *Server) registerAuthRoutes(router *gin.Engine) {
	if s.accounts == nil || s.sessions == nil || s.users == nil {
		return
	}
	// 登录/注册/引导是登录前流程：此时还没有服务端会话，会话绑定的 CSRF token 无从产生，
	// 因此这三个 POST 只依赖全局的会话解析中间件，不挂 CSRFMiddleware。
	// token 只能覆盖已有会话的请求（DESIGN.md §4.3）；SameSite=Lax 仍是同一层防护。
	router.GET("/login", s.loginPage)
	router.POST("/login", s.loginSubmit)
	router.GET("/register", s.registerPage)
	router.POST("/register", s.registerSubmit)
	router.GET("/setup", s.setupPage)
	router.POST("/setup", s.setupSubmit)
	// 登出是登录后流程：会话已存在，必须携带会话绑定的 CSRF token（DESIGN.md §4.3）。
	router.POST("/logout", s.sessions.CSRFMiddleware(), s.logout)
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
func (s *Server) authLayout(loc *i18n.Localizer, titleKey string) views.LayoutData {
	return views.LayoutData{
		Lang:       loc.Locale(),
		Title:      loc.T(titleKey),
		Brand:      loc.T("app.name"),
		HomeURL:    "/",
		Footer:     loc.T("footer.powered_by"),
		CSSURL:     s.assets.URL("css/tailwind.css"),
		HTMXURL:    s.assets.URL("js/htmx.min.js"),
		MathJaxURL: s.assets.URL("js/mathjax/tex-svg.js"),
	}
}

// languageOptionsFor 生成指向当前路径的语言切换入口；认证页不能复用首页的 "/?lang=" 逻辑。
func (s *Server) languageOptionsFor(loc *i18n.Localizer, path string) []views.LanguageOption {
	codes := s.i18n.SupportedCodes()
	out := make([]views.LanguageOption, 0, len(codes))
	for _, code := range codes {
		out = append(out, views.LanguageOption{
			Label:  loc.T("language." + code),
			Href:   path + "?lang=" + code,
			Active: loc.Locale() == code,
		})
	}
	return out
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
	s.renderAuth(c, status, views.AuthFormData{
		Layout:               s.authLayout(loc, "auth.login.title"),
		Heading:              loc.T("auth.login.heading"),
		Action:               "/login",
		SubmitLabel:          loc.T("auth.login.submit"),
		ErrorMessage:         errMsg,
		UsernameLabel:        loc.T("auth.field.username"),
		PasswordLabel:        loc.T("auth.field.password"),
		PasswordAutocomplete: "current-password",
		AltLabel:             loc.T("auth.login.to_register"),
		AltHref:              "/register",
		LangOptions:          s.languageOptionsFor(loc, c.Request.URL.Path),
	})
}

// loginSubmit 校验凭据、建立服务端会话并重定向到首页；失败时回填错误提示而不是跳转。
func (s *Server) loginSubmit(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	username := strings.TrimSpace(c.PostForm("username"))
	u, err := s.accounts.Authenticate(c.Request.Context(), username, c.PostForm("password"))
	if err != nil {
		// 只记用户名与错误，绝不记录密码（AGENTS.md §2.1：日志英文）。
		s.logger.Info("login failed", "username", username, "error", err)
		key := "auth.error.invalid_credentials"
		if errors.Is(err, auth.ErrUserDisabled) {
			key = "auth.error.user_disabled"
		}
		s.renderLogin(c, loc, http.StatusUnauthorized, loc.T(key))
		return
	}
	if _, err := s.sessions.StartSession(c.Request.Context(), c, u.ID); err != nil {
		s.logger.Error("start session failed", "user_id", u.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Redirect(http.StatusSeeOther, "/")
}

// registerPage 渲染自助注册表单。
func (s *Server) registerPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	s.renderRegister(c, loc, http.StatusOK, "")
}

// renderRegister 渲染注册表单并带上一条已本地化的错误提示（可为空）。
func (s *Server) renderRegister(c *gin.Context, loc *i18n.Localizer, status int, errMsg string) {
	s.renderAuth(c, status, views.AuthFormData{
		Layout:               s.authLayout(loc, "auth.register.title"),
		Heading:              loc.T("auth.register.heading"),
		Intro:                loc.T("auth.register.intro"),
		Action:               "/register",
		SubmitLabel:          loc.T("auth.register.submit"),
		ErrorMessage:         errMsg,
		UsernameLabel:        loc.T("auth.field.username"),
		EmailLabel:           loc.T("auth.field.email"),
		PasswordLabel:        loc.T("auth.field.password"),
		DisplayNameLabel:     loc.T("auth.field.display_name"),
		ShowEmail:            true,
		ShowDisplayName:      true,
		PasswordAutocomplete: "new-password",
		AltLabel:             loc.T("auth.register.to_login"),
		AltHref:              "/login",
		LangOptions:          s.languageOptionsFor(loc, c.Request.URL.Path),
	})
}

// registrationPermission 决定当前是否允许自助注册，以及新账号的角色。
//
// 完整的注册策略（settings 里的 open / invite / closed 加邮箱域名白名单）由 M1-6 接管；
// 本轮按 DESIGN.md §4.2 默认 closed：只有"尚不存在管理员"时放行，且该账号成为管理员
// —— 这正是 closed 策略下唯一的合法放行路径（首个管理员引导，DESIGN.md §4.1）。
func (s *Server) registrationPermission(c *gin.Context) (allowed bool, role string, err error) {
	n, err := s.users.CountActiveAdmins(c.Request.Context())
	if err != nil {
		return false, "", err
	}
	if n == 0 {
		return true, store.RoleAdmin, nil
	}
	return false, "", nil
}

// registerSubmit 创建本地账号；失败时把校验错误渲染回表单。
func (s *Server) registerSubmit(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	allowed, role, err := s.registrationPermission(c)
	if err != nil {
		s.logger.Error("count active admins", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if !allowed {
		// M1-6 会按策略给出更具体的提示；当前统一提示"自助注册已关闭"。
		s.renderRegister(c, loc, http.StatusForbidden, loc.T("auth.error.registration_closed"))
		return
	}
	username := strings.TrimSpace(c.PostForm("username"))
	email := strings.ToLower(strings.TrimSpace(c.PostForm("email")))
	display := strings.TrimSpace(c.PostForm("display_name"))
	password := c.PostForm("password")
	if msg := validateRegisterInput(loc, username, email, password); msg != "" {
		s.renderRegister(c, loc, http.StatusBadRequest, msg)
		return
	}
	_, err = s.accounts.CreateLocalUser(ctx, auth.CreateUserInput{
		Username:    username,
		Email:       email,
		DisplayName: display,
		Password:    password,
		Role:        role,
		Locale:      loc.Locale(),
	})
	if err != nil {
		s.logger.Error("create local user failed", "username", username, "error", err)
		s.renderRegister(c, loc, http.StatusConflict, loc.T("auth.error.create_failed"))
		return
	}
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
		Layout:               s.authLayout(loc, "auth.setup.title"),
		Heading:              loc.T("auth.setup.heading"),
		Intro:                loc.T("auth.setup.intro"),
		Action:               "/setup",
		SubmitLabel:          loc.T("auth.setup.submit"),
		ErrorMessage:         errMsg,
		UsernameLabel:        loc.T("auth.field.username"),
		EmailLabel:           loc.T("auth.field.email"),
		PasswordLabel:        loc.T("auth.field.password"),
		DisplayNameLabel:     loc.T("auth.field.display_name"),
		ShowEmail:            true,
		ShowDisplayName:      true,
		EmailValue:           s.bootstrapEmail,
		PasswordAutocomplete: "new-password",
		LangOptions:          s.languageOptionsFor(loc, c.Request.URL.Path),
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
	_, err := s.accounts.CreateLocalUser(ctx, auth.CreateUserInput{
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
	c.Redirect(http.StatusSeeOther, "/login")
}

// logout 作废当前会话并清除 cookie，然后回到登录页。
func (s *Server) logout(c *gin.Context) {
	if err := s.sessions.Logout(c.Request.Context(), c); err != nil {
		s.logger.Error("logout failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
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
