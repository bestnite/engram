package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"example.com/engram/internal/auth"
	"example.com/engram/internal/i18n"
	"example.com/engram/internal/mail"
	"example.com/engram/internal/store"
	"example.com/engram/internal/web/views"
)

// 本文件是 M1-19 的密码重置流程：请求重置（发信）与凭链接设置新密码。
//
// 安全要点（DESIGN.md §4.7）：
//   - 明文令牌只出现在邮件链接里，绝不入库、绝不进日志（日志只记 user_id 与结果）；
//   - 令牌库中只存摘要，一次性（条件更新），有 1 小时过期；
//   - 请求重置一律回同一页（sent），不因邮箱是否存在而不同，避免账号枚举；
//   - SMTP 未配置时页面渲染 mail.not_configured，POST 也回同一说明，绝不静默。

// registerSecurityMailRoutes 挂载 M1-19 的密码重置、邮箱验证与改邮箱确认路由。
// 依赖未装配时跳过，保证 M0 阶段的测试仍能构造 Server。
func (s *Server) registerSecurityMailRoutes(router *gin.Engine) {
	if s.tokens == nil {
		return
	}
	// 登录前流程：没有会话可绑 CSRF，用双提交 cookie（B-13），与 /login 一致。
	router.GET("/forgot-password", s.forgotPasswordPage)
	router.POST("/forgot-password", auth.DoubleSubmitMiddleware(), s.forgotPasswordSubmit)
	router.GET("/reset-password", s.resetPasswordPage)
	router.POST("/reset-password", auth.DoubleSubmitMiddleware(), s.resetPasswordSubmit)
	// 邮箱验证与改邮箱确认：验证链接免登录（凭令牌），改邮箱请求需登录。
	router.GET("/verify-email", s.verifyEmail)
	router.GET("/confirm-email-change", s.confirmEmailChange)
	router.GET("/settings/email", s.emailChangePage)
	if s.sessions != nil {
		router.POST("/settings/email", s.sessions.CSRFMiddleware(), s.emailChangeSubmit)
		router.POST("/settings/verify-email", s.sessions.CSRFMiddleware(), s.resendVerificationSubmit)
	}
}

// renderSecurityForm 写出安全/事务页面；文案全部来自语言包。
func (s *Server) renderSecurityForm(c *gin.Context, loc *i18n.Localizer, status int, data views.SecurityFormData) {
	data.LangOptions = s.languageOptionsFor(loc, c.Request.URL.Path)
	renderHTMLStatus(c, status, views.SecurityPage(data))
}

// forgotPasswordPage 渲染「请求重置」表单；SMTP 未配置时改为渲染 mail.not_configured。
func (s *Server) forgotPasswordPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	data := views.SecurityFormData{
		Layout:      s.authLayout(loc, "mail.reset.title"),
		Heading:     loc.T("mail.reset.heading"),
		Intro:       loc.T("mail.reset.intro"),
		Action:      "/forgot-password",
		CSRF:        auth.EnsureDoubleSubmitToken(c),
		ShowEmail:   true,
		EmailLabel:  loc.T("mail.reset.email_label"),
		SubmitLabel: loc.T("mail.reset.submit"),
		AltLabel:    loc.T("mail.reset.back_login"),
		AltHref:     "/login",
	}
	switch {
	case !s.securityMailReady():
		// 未配置：明确说明原因，不给出一个按了也没反应的按钮（DESIGN.md §4.7）。
		data.Notice = loc.T("mail.not_configured")
	case c.Query("sent") == "1":
		data.Notice = loc.T("mail.reset.sent")
		data.ShowForm = true
	default:
		data.ShowForm = true
	}
	s.renderSecurityForm(c, loc, http.StatusOK, data)
}

// forgotPasswordSubmit 处理重置请求：找到账号就签发一次性令牌并发信，
// 但无论账号是否存在都回到同一页（sent），避免账号枚举。
func (s *Server) forgotPasswordSubmit(c *gin.Context) {
	if _, ok := s.localizer(c); !ok {
		return
	}
	if !s.securityMailReady() {
		// 未配置：回到页面并渲染 mail.not_configured，绝不静默什么都不做。
		c.Redirect(http.StatusSeeOther, "/forgot-password")
		return
	}
	ctx := c.Request.Context()
	email := strings.ToLower(strings.TrimSpace(c.PostForm("email")))
	if email != "" {
		if u, err := s.users.ByEmail(ctx, email); err == nil {
			s.issuePasswordReset(c, u)
		} else if !store.IsNotFound(err) {
			s.logger.Error("security mail: load user for password reset failed", "error", err)
		}
	}
	c.Redirect(http.StatusSeeOther, "/forgot-password?sent=1")
}

// issuePasswordReset 签发密码重置令牌并投递邮件；任何失败只记英文日志（绝不回传）。
// 日志绝不包含令牌明文。
func (s *Server) issuePasswordReset(c *gin.Context, u *store.User) {
	ctx := c.Request.Context()
	now := time.Now().UTC()
	token, err := s.tokens.Issue(ctx, u.ID, store.ActionTokenPasswordReset, "", auth.PasswordResetTTL)
	if err != nil {
		s.logger.Error("security mail: issue password reset token failed", "user_id", u.ID, "error", err)
		return
	}
	loc := s.userLocalizer(u)
	link := s.securityAbsoluteURL(c, "/reset-password?token="+url.QueryEscape(token))
	msg := passwordResetMessage(loc, u.Email, link, securitySiteName(loc), now.Add(auth.PasswordResetTTL))
	s.sendSecurity(ctx, u.ID, msg.To, mail.TypePasswordReset, msg.Subject, msg.TextBody, msg.HTMLBody)
}

// resetPasswordPage 渲染「设置新密码」表单；缺 token 时给出无效提示。
func (s *Server) resetPasswordPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	token := strings.TrimSpace(c.Query("token"))
	if token == "" {
		s.renderResetResult(c, loc, http.StatusBadRequest, loc.T("mail.reset.error_invalid"))
		return
	}
	// 令牌放进表单 action 的查询串，避免额外隐藏字段，也保证 POST 时原样带回。
	s.renderSecurityForm(c, loc, http.StatusOK, views.SecurityFormData{
		Layout:        s.authLayout(loc, "mail.reset.form_title"),
		Heading:       loc.T("mail.reset.form_heading"),
		Intro:         loc.T("mail.reset.form_intro"),
		ShowForm:      true,
		Action:        "/reset-password?token=" + url.QueryEscape(token),
		CSRF:          auth.EnsureDoubleSubmitToken(c),
		ShowPassword:  true,
		PasswordLabel: loc.T("mail.reset.new_password_label"),
		SubmitLabel:   loc.T("mail.reset.form_submit"),
		AltLabel:      loc.T("mail.reset.back_login"),
		AltHref:       "/login",
	})
}

// resetPasswordSubmit 消费一次性令牌并设置新密码；令牌用过、过期或不存在都给出稳定提示。
func (s *Server) resetPasswordSubmit(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	token := strings.TrimSpace(c.PostForm("token"))
	if token == "" {
		token = strings.TrimSpace(c.Query("token"))
	}
	tok, err := s.tokens.Consume(ctx, store.ActionTokenPasswordReset, token)
	if err != nil {
		s.renderResetResult(c, loc, http.StatusBadRequest, loc.T(resetTokenErrorKey(err)))
		return
	}
	newPassword := c.PostForm("password")
	if msg := validateNewPassword(loc, newPassword); msg != "" {
		// 密码不合规时令牌已被消费——这是刻意的：一次性令牌用掉即废，
		// 用户重新走一次「忘记密码」即可，不留下可反复尝试的窗口。
		s.renderResetResult(c, loc, http.StatusBadRequest, msg)
		return
	}
	if err := s.accounts.SetPasswordFromReset(ctx, tok.UserID, newPassword); err != nil {
		s.logger.Error("security mail: set password from reset failed", "user_id", tok.UserID, "error", err)
		s.renderResetResult(c, loc, http.StatusBadRequest, loc.T("mail.reset.error_invalid"))
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(tok.UserID),
		Action:     store.ActionUserPasswordResetComplete,
		TargetType: "user",
		TargetID:   store.Ptr(tok.UserID),
	})
	s.renderResetResult(c, loc, http.StatusOK, loc.T("mail.reset.done"))
}

// renderResetResult 渲染重置流程的结果页（无表单）。
func (s *Server) renderResetResult(c *gin.Context, loc *i18n.Localizer, status int, notice string) {
	data := views.SecurityFormData{
		Layout:   s.authLayout(loc, "mail.reset.title"),
		Heading:  loc.T("mail.reset.heading"),
		AltLabel: loc.T("mail.reset.back_login"),
		AltHref:  "/login",
	}
	if status == http.StatusOK {
		data.Notice = notice
	} else {
		data.ErrorMessage = notice
	}
	s.renderSecurityForm(c, loc, status, data)
}

// resetTokenErrorKey 把令牌消费错误映射到稳定语言包 key。
func resetTokenErrorKey(err error) string {
	switch {
	case errors.Is(err, store.ErrActionTokenExpired):
		return "mail.reset.error_expired"
	case errors.Is(err, store.ErrActionTokenUsed):
		return "mail.reset.error_used"
	default:
		return "mail.reset.error_invalid"
	}
}

// validateNewPassword 校验新密码强度并返回已本地化提示；通过时返回空串。
func validateNewPassword(loc *i18n.Localizer, password string) string {
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
