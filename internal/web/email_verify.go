package web

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
)

// 本文件是 M1-19 的邮箱验证与改邮箱确认流程。
//
//   - 邮箱验证：注册/管理员建号后发信，用户点链接把 email_verified_at 置为当前时间；
//   - 改邮箱：登录用户在设置页提交新地址，确认信发到「新地址」，点链接后才真正改库；
//   - 两种令牌都走同一张 action_tokens 表：只存摘要、一次性、有过期。
//
// 免登录的验证/确认链接直接消费令牌；改邮箱的请求需要登录并过 CSRF。

// verifyEmail 消费邮箱验证令牌并标记邮箱已验证。
func (s *Server) verifyEmail(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	token := strings.TrimSpace(c.Query("token"))
	tok, err := s.tokens.Consume(ctx, store.ActionTokenEmailVerify, token)
	if err != nil {
		s.renderSecurityResult(c, loc, http.StatusBadRequest,
			"mail.verify.title", "mail.verify.heading", "mail.verify.back_login",
			loc.T(actionTokenErrorKey("mail.verify.error_", err)), false)
		return
	}
	now := time.Now().UTC()
	if err := s.users.SetEmailVerifiedAt(ctx, tok.UserID, &now); err != nil {
		s.logger.Error("security mail: mark email verified failed", "user_id", tok.UserID, "error", err)
		s.renderSecurityResult(c, loc, http.StatusInternalServerError,
			"mail.verify.title", "mail.verify.heading", "mail.verify.back_login",
			loc.T("mail.verify.error_invalid"), false)
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(tok.UserID),
		Action:     store.ActionUserEmailVerified,
		TargetType: "user",
		TargetID:   store.Ptr(tok.UserID),
	})
	s.renderSecurityResult(c, loc, http.StatusOK,
		"mail.verify.title", "mail.verify.heading", "mail.verify.back_login",
		loc.T("mail.verify.done"), true)
}

// confirmEmailChange 消费改邮箱令牌并真正更新邮箱。
func (s *Server) confirmEmailChange(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	token := strings.TrimSpace(c.Query("token"))
	tok, err := s.tokens.Consume(ctx, store.ActionTokenEmailChange, token)
	if err != nil {
		s.renderSecurityResult(c, loc, http.StatusBadRequest,
			"mail.verify.change_title", "mail.verify.change_heading", "mail.verify.back_login",
			loc.T(actionTokenErrorKey("mail.verify.change_error_", err)), false)
		return
	}
	newEmail := strings.ToLower(strings.TrimSpace(tok.Payload))
	if newEmail == "" {
		s.renderSecurityResult(c, loc, http.StatusBadRequest,
			"mail.verify.change_title", "mail.verify.change_heading", "mail.verify.back_login",
			loc.T("mail.verify.change_error_invalid"), false)
		return
	}
	now := time.Now().UTC()
	if err := s.users.SetEmail(ctx, tok.UserID, newEmail, now); err != nil {
		s.logger.Error("security mail: update email from confirmation failed", "user_id", tok.UserID, "error", err)
		s.renderSecurityResult(c, loc, http.StatusConflict,
			"mail.verify.change_title", "mail.verify.change_heading", "mail.verify.back_login",
			loc.T("mail.verify.change_error_invalid"), false)
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(tok.UserID),
		Action:     store.ActionUserEmailChanged,
		TargetType: "user",
		TargetID:   store.Ptr(tok.UserID),
		Detail:     map[string]any{"email": newEmail},
	})
	s.renderSecurityResult(c, loc, http.StatusOK,
		"mail.verify.change_title", "mail.verify.change_heading", "mail.verify.back_login",
		loc.T("mail.verify.change_done"), true)
}

// emailChangePage 渲染改邮箱表单（登录用户）；SMTP 未配置时渲染 mail.not_configured。
func (s *Server) emailChangePage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	data := views.SecurityFormData{
		Layout:      s.pageLayout(c, loc, "mail.verify.change_title"),
		Heading:     loc.T("mail.verify.change_heading"),
		Intro:       loc.T("mail.verify.change_intro"),
		Action:      "/settings/email",
		CSRF:        sessionCSRF(c),
		ShowEmail:   true,
		EmailLabel:  loc.T("mail.verify.change_email_label"),
		EmailValue:  user.Email,
		SubmitLabel: loc.T("mail.verify.change_submit"),
		AltLabel:    loc.T("mail.verify.back_settings"),
		AltHref:     "/settings",
	}
	if !s.securityMailReady() {
		data.Notice = loc.T("mail.not_configured")
	} else {
		data.ShowForm = true
	}
	s.renderSecurityForm(c, loc, http.StatusOK, data)
}

// emailChangeSubmit 提交改邮箱请求：向新地址发确认信，确认前不改库。
func (s *Server) emailChangeSubmit(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	newEmail := strings.ToLower(strings.TrimSpace(c.PostForm("email")))
	renderErr := func(msg string) {
		s.renderSecurityForm(c, loc, http.StatusBadRequest, views.SecurityFormData{
			Layout:       s.pageLayout(c, loc, "mail.verify.change_title"),
			Heading:      loc.T("mail.verify.change_heading"),
			Intro:        loc.T("mail.verify.change_intro"),
			ErrorMessage: msg,
			ShowForm:     true,
			Action:       "/settings/email",
			CSRF:         sessionCSRF(c),
			ShowEmail:    true,
			EmailLabel:   loc.T("mail.verify.change_email_label"),
			EmailValue:   newEmail,
			SubmitLabel:  loc.T("mail.verify.change_submit"),
			AltLabel:     loc.T("mail.verify.back_settings"),
			AltHref:      "/settings",
		})
	}
	if !s.securityMailReady() {
		renderErr(loc.T("mail.not_configured"))
		return
	}
	if _, err := mail.ParseAddress(newEmail); err != nil {
		renderErr(loc.T("auth.error.email_invalid"))
		return
	}
	if newEmail == strings.ToLower(strings.TrimSpace(user.Email)) {
		renderErr(loc.T("mail.verify.change_same_email"))
		return
	}
	if existing, err := s.users.ByEmail(ctx, newEmail); err == nil && existing != nil {
		renderErr(loc.T("mail.verify.change_email_taken"))
		return
	} else if err != nil && !store.IsNotFound(err) {
		s.logger.Error("security mail: check email availability failed", "error", err)
		renderErr(loc.T("mail.verify.change_error_invalid"))
		return
	}
	if !s.sendEmailChangeConfirmation(c, user, newEmail) {
		renderErr(loc.T("mail.not_configured"))
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionUserEmailChangeRequest,
		TargetType: "user",
		TargetID:   store.Ptr(user.ID),
		Detail:     map[string]any{"email": newEmail},
	})
	s.renderSecurityResult(c, loc, http.StatusOK,
		"mail.verify.change_title", "mail.verify.change_heading", "mail.verify.back_settings",
		loc.T("mail.verify.change_sent"), true)
}

// resendVerificationSubmit 重发邮箱验证邮件（登录用户）。
func (s *Server) resendVerificationSubmit(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	if !s.securityMailReady() {
		s.renderSecurityResult(c, loc, http.StatusOK,
			"mail.verify.title", "mail.verify.heading", "mail.verify.back_settings",
			loc.T("mail.not_configured"), false)
		return
	}
	if !s.sendEmailVerification(c, user) {
		s.renderSecurityResult(c, loc, http.StatusOK,
			"mail.verify.title", "mail.verify.heading", "mail.verify.back_settings",
			loc.T("mail.not_configured"), false)
		return
	}
	s.audit(c.Request.Context(), store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionUserEmailVerifyRequest,
		TargetType: "user",
		TargetID:   store.Ptr(user.ID),
	})
	s.renderSecurityResult(c, loc, http.StatusOK,
		"mail.verify.title", "mail.verify.heading", "mail.verify.back_settings",
		loc.T("mail.verify.sent"), true)
}

// renderSecurityResult 渲染结果页（无表单）；ok=true 走 Notice，否则走 ErrorMessage。
func (s *Server) renderSecurityResult(c *gin.Context, loc *i18n.Localizer, status int, titleKey, headingKey, altKey, msg string, ok bool) {
	data := views.SecurityFormData{
		Layout:   s.pageLayout(c, loc, titleKey),
		Heading:  loc.T(headingKey),
		AltLabel: loc.T(altKey),
		AltHref:  "/settings",
	}
	if !ok {
		data.AltHref = "/login"
	}
	if ok {
		data.Notice = msg
	} else {
		data.ErrorMessage = msg
	}
	s.renderSecurityForm(c, loc, status, data)
}

// actionTokenErrorKey 把令牌消费错误映射到带前缀的稳定语言包 key。
// 这是唯一的映射实现：verify / reset / unsubscribe 三条流程都传各自的前缀调用它。
func actionTokenErrorKey(prefix string, err error) string {
	switch {
	case errors.Is(err, store.ErrActionTokenExpired):
		return prefix + "expired"
	case errors.Is(err, store.ErrActionTokenUsed):
		return prefix + "used"
	default:
		return prefix + "invalid"
	}
}
