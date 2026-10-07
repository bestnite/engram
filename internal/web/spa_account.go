package web

import (
	"errors"
	"net/http"
	netmail "net/mail"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是账号安全与邮件流程（M1-19）的 SPA 同源 JSON 传输层（DESIGN.md §4.3、§4.7、§8.1）。
//
// 它只新增「传输」：令牌签发/消费、一次性语义、匿名限流、改密码后的密钥作废与审计行全部复用既有
// 服务逻辑。可导航的读取页（/forgot-password、/reset-password、/settings/email）与邮件里的一键
// GET 链接（/verify-email、/confirm-email-change）都只返回应用壳，页面与令牌消费全部走这里的
// JSON 端点；这些页面因此没有 SSR 回退。

// registerSPAAccountRoutes 挂载账号安全与邮件流程的 SPA JSON 端点与应用壳入口。
// 令牌服务未装配时跳过，与 registerSecurityMailRoutes 的前置条件一致。
func (s *Server) registerSPAAccountRoutes(router *gin.Engine) {
	if s.tokens == nil {
		return
	}
	// 登录前流程：没有会话可绑 CSRF，用双提交 cookie（与 SSR 的 /forgot-password、/reset-password 一致）。
	router.POST("/api/v1/auth/forgot-password", auth.DoubleSubmitMiddleware(), s.spaForgotPasswordSubmit)
	router.POST("/api/v1/auth/reset-password", auth.DoubleSubmitMiddleware(), s.spaResetPasswordSubmit)
	router.POST("/api/v1/auth/verify-email", auth.DoubleSubmitMiddleware(), s.spaVerifyEmailSubmit)
	router.POST("/api/v1/auth/confirm-email-change", auth.DoubleSubmitMiddleware(), s.spaConfirmEmailChangeSubmit)
	// SPA 应用壳入口：与 /spa/login 同一约定，不遮蔽 SSR 的免登录一键链接。
	router.GET("/spa/verify-email", s.spaVerifyEmailShell)
	router.GET("/spa/confirm-email-change", s.spaConfirmEmailChangeShell)
	if s.sessions != nil {
		router.GET("/api/v1/settings/email", s.spaEmailSettingsGet)
		router.POST("/api/v1/settings/email", s.sessions.CSRFMiddleware(), s.spaEmailChangeSubmit)
		router.POST("/api/v1/settings/verify-email", s.sessions.CSRFMiddleware(), s.spaResendVerificationSubmit)
	}
}

// ── 应用壳入口（页面只返回应用壳，读写走 JSON 端点）──────────────────────────

// forgotPasswordRoute 提供 GET /forgot-password：下发会话前双提交 cookie 并返回应用壳，
// 由客户端路由渲染「请求重置」页；请求重置协议走 POST /api/v1/auth/forgot-password（同一份服务逻辑）。
// SMTP 未配置的说明由响应的 mail_ready 字段驱动，不再由服务端渲染。
func (s *Server) forgotPasswordRoute(c *gin.Context) {
	auth.EnsureDoubleSubmitToken(c, s.secureCookies())
	s.spa.ServeIndex(c)
}

// resetPasswordRoute 提供 GET /reset-password：下发会话前双提交 cookie 并返回应用壳，
// 由客户端路由从 URL 读取 token 渲染「设置新密码」页；提交走 POST /api/v1/auth/reset-password。
// token 缺失或无效的判定由 JSON 端点给出。
func (s *Server) resetPasswordRoute(c *gin.Context) {
	auth.EnsureDoubleSubmitToken(c, s.secureCookies())
	s.spa.ServeIndex(c)
}

// emailChangeRoute 提供 GET /settings/email：返回应用壳，由客户端路由渲染改邮箱页；
// 读取与提交走 /api/v1/settings/email（同一份服务逻辑）。授权判定与迁移前逐项一致——这是登录用户
// 自己的页面，未登录一律重定向登录页，切壳前先过 requireUser，绝不因切壳放开。
func (s *Server) emailChangeRoute(c *gin.Context) {
	if _, ok := s.requireUser(c); !ok {
		return
	}
	s.spa.ServeIndex(c)
}

// spaVerifyEmailShell 是邮箱验证的规范入口（GET /verify-email）。邮件里的一键链接就指向这里，
// 由 SPA 读取 ?token= 并通过 POST /api/v1/auth/verify-email 消费（一次性、有过期）。
// 像 /spa/login 一样先下发会话前双提交 cookie，再返回应用壳；这里只返回壳，不消费任何令牌。
func (s *Server) spaVerifyEmailShell(c *gin.Context) {
	auth.EnsureDoubleSubmitToken(c, s.secureCookies())
	s.spa.ServeIndex(c)
}

// spaConfirmEmailChangeShell 是改邮箱确认的规范入口（GET /confirm-email-change）。
// 与 /verify-email 同构：下发双提交 cookie 并返回应用壳，确认协议走
// POST /api/v1/auth/confirm-email-change。这里只返回壳，不消费令牌。
func (s *Server) spaConfirmEmailChangeShell(c *gin.Context) {
	auth.EnsureDoubleSubmitToken(c, s.secureCookies())
	s.spa.ServeIndex(c)
}

// ── JSON 端点（会话前）──────────────────────────────────────────────────────

// apiForgotPasswordRequest 是 POST /api/v1/auth/forgot-password 的请求体。
type apiForgotPasswordRequest struct {
	Email string `json:"email"`
}

// spaForgotPasswordSubmit 处理 SPA 的重置请求（POST /api/v1/auth/forgot-password）。
//
// 与 SSR 的 forgotPasswordSubmit 同一份判定：匿名限流（IP + 目标邮箱各 5 次 / 15 分钟）在查库与
// 入 outbox 之前；无论邮箱是否存在都返回同形响应，避免账号枚举。响应里的 mail_ready 只反映站点
// 级的「邮件是否配置」，与账号存在性无关，前端据此在未配置时渲染 mail.not_configured 而不是
// 谎报「已发送」。这里不返回令牌、不回显邮箱。
func (s *Server) spaForgotPasswordSubmit(c *gin.Context) {
	var req apiForgotPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiAuthError(c, http.StatusBadRequest, api.CodeInvalidRequest, "The request is invalid.")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if s.anonRateLimited(c, email) {
		apiAuthError(c, http.StatusTooManyRequests, "rate_limited", "Too many requests. Try again later.")
		return
	}
	ctx := c.Request.Context()
	mailReady := s.securityMailReady()
	if mailReady && email != "" {
		if u, err := s.users.ByEmail(ctx, email); err == nil {
			s.issuePasswordReset(c, u)
		} else if !store.IsNotFound(err) {
			s.logger.Error("security mail: load user for password reset failed", "error", err)
		}
	}
	c.JSON(http.StatusOK, gin.H{"mail_ready": mailReady})
}

// apiResetPasswordRequest 是 POST /api/v1/auth/reset-password 的请求体。
type apiResetPasswordRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// spaResetPasswordSubmit 消费一次性重置令牌并设置新密码（POST /api/v1/auth/reset-password）。
//
// 令牌只由服务端签发并只存摘要，消费是条件更新（一次性）；密码不合规时令牌已被消费——这是刻意的，
// 与 SSR 一致：一次性令牌用掉即废，用户重新走一次「忘记密码」即可，不留可反复尝试的窗口。
// 改密码的密钥作废由 SetPasswordFromResetTx 承担；审计行与 SSR 完全相同。
func (s *Server) spaResetPasswordSubmit(c *gin.Context) {
	var req apiResetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiAuthError(c, http.StatusBadRequest, api.CodeInvalidRequest, "The request is invalid.")
		return
	}
	ctx := c.Request.Context()
	token := strings.TrimSpace(req.Token)
	if token == "" {
		apiAuthError(c, http.StatusBadRequest, "token_invalid", "The reset link is invalid.")
		return
	}
	tok, err := s.tokens.Consume(ctx, store.ActionTokenPasswordReset, token)
	if err != nil {
		apiAuthError(c, http.StatusBadRequest, actionTokenErrorCode(err), "The reset link is invalid or has expired.")
		return
	}
	if code := passwordPolicyErrorCode(req.Password); code != "" {
		apiAuthError(c, http.StatusBadRequest, code, passwordPolicyErrorMessage(code))
		return
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return s.accounts.SetPasswordFromResetTx(ctx, tx, tok.UserID, req.Password)
	})
	if err != nil {
		s.logger.Error("security mail: set password from reset failed", "user_id", tok.UserID, "error", err)
		apiAuthError(c, http.StatusBadRequest, "token_invalid", "The reset link is invalid.")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(tok.UserID),
		Action:     store.ActionUserPasswordResetComplete,
		TargetType: "user",
		TargetID:   store.Ptr(tok.UserID),
	})
	c.JSON(http.StatusOK, gin.H{"reset": true})
}

// apiTokenOnlyRequest 是只带一枚令牌的请求体（验证邮箱、确认改邮箱）。
type apiTokenOnlyRequest struct {
	Token string `json:"token"`
}

// spaVerifyEmailSubmit 消费邮箱验证令牌并标记邮箱已验证（POST /api/v1/auth/verify-email）。
// 令牌语义与 SSR 的 verifyEmail 完全一致：一次性、有过期；成功写审计，失败返回稳定 code。
func (s *Server) spaVerifyEmailSubmit(c *gin.Context) {
	var req apiTokenOnlyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiAuthError(c, http.StatusBadRequest, api.CodeInvalidRequest, "The request is invalid.")
		return
	}
	ctx := c.Request.Context()
	tok, err := s.tokens.Consume(ctx, store.ActionTokenEmailVerify, strings.TrimSpace(req.Token))
	if err != nil {
		apiAuthError(c, http.StatusBadRequest, actionTokenErrorCode(err), "The verification link is invalid or has expired.")
		return
	}
	now := time.Now().UTC()
	if err := s.users.SetEmailVerifiedAt(ctx, tok.UserID, &now); err != nil {
		s.logger.Error("security mail: mark email verified failed", "user_id", tok.UserID, "error", err)
		apiAuthError(c, http.StatusInternalServerError, api.CodeInternal, "An internal error occurred.")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(tok.UserID),
		Action:     store.ActionUserEmailVerified,
		TargetType: "user",
		TargetID:   store.Ptr(tok.UserID),
	})
	c.JSON(http.StatusOK, gin.H{"verified": true})
}

// spaConfirmEmailChangeSubmit 消费改邮箱令牌并真正更新邮箱（POST /api/v1/auth/confirm-email-change）。
// 令牌 Payload 保存待确认的新邮箱；确认成功写审计，新邮箱冲突返回 409（与 SSR 一致）。
func (s *Server) spaConfirmEmailChangeSubmit(c *gin.Context) {
	var req apiTokenOnlyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiAuthError(c, http.StatusBadRequest, api.CodeInvalidRequest, "The request is invalid.")
		return
	}
	ctx := c.Request.Context()
	tok, err := s.tokens.Consume(ctx, store.ActionTokenEmailChange, strings.TrimSpace(req.Token))
	if err != nil {
		apiAuthError(c, http.StatusBadRequest, actionTokenErrorCode(err), "The confirmation link is invalid or has expired.")
		return
	}
	newEmail := strings.ToLower(strings.TrimSpace(tok.Payload))
	if newEmail == "" {
		apiAuthError(c, http.StatusBadRequest, "token_invalid", "The confirmation link is invalid.")
		return
	}
	now := time.Now().UTC()
	if err := s.users.SetEmail(ctx, tok.UserID, newEmail, now); err != nil {
		s.logger.Error("security mail: update email from confirmation failed", "user_id", tok.UserID, "error", err)
		apiAuthError(c, http.StatusConflict, "email_conflict", "That email address could not be used.")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(tok.UserID),
		Action:     store.ActionUserEmailChanged,
		TargetType: "user",
		TargetID:   store.Ptr(tok.UserID),
		Detail:     map[string]any{"email": newEmail},
	})
	c.JSON(http.StatusOK, gin.H{"changed": true})
}

// ── JSON 端点（会话）────────────────────────────────────────────────────────

// spaEmailSettingsGet 返回当前用户的邮箱、验证状态与站点邮件是否可用（GET /api/v1/settings/email）。
// 仅接受浏览器会话；会话缺失时由 spaProfileSessionOnly 返回 401。绝不返回任何其它用户数据。
func (s *Server) spaEmailSettingsGet(c *gin.Context) {
	user, ok := s.spaProfileSessionOnly(c)
	if !ok {
		return
	}
	fresh, err := s.users.ByID(c.Request.Context(), user.ID)
	if err != nil {
		s.logger.Error("load user for email settings failed", "user_id", user.ID, "error", err)
		apiAuthError(c, http.StatusInternalServerError, api.CodeInternal, "An internal error occurred.")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"email":          fresh.Email,
		"email_verified": fresh.EmailVerifiedAt != nil,
		"mail_ready":     s.securityMailReady(),
	})
}

// apiEmailChangeRequest 是 POST /api/v1/settings/email 的请求体。
type apiEmailChangeRequest struct {
	Email string `json:"email"`
}

// spaEmailChangeSubmit 提交改邮箱请求（POST /api/v1/settings/email）：向新地址发确认信，确认前不改库。
//
// 与 SSR 的 emailChangeSubmit 逐项同判定：SMTP 未配置、格式非法、与原邮箱相同、已被占用都拒绝且
// 不写库；成功只入队确认信并写审计（ActionUserEmailChangeRequest），库中的邮箱在点击确认链接前不变。
func (s *Server) spaEmailChangeSubmit(c *gin.Context) {
	user, ok := s.spaProfileSessionOnly(c)
	if !ok {
		return
	}
	var req apiEmailChangeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiAuthError(c, http.StatusBadRequest, api.CodeInvalidRequest, "The request is invalid.")
		return
	}
	ctx := c.Request.Context()
	newEmail := strings.ToLower(strings.TrimSpace(req.Email))
	if !s.securityMailReady() {
		apiAuthError(c, http.StatusBadRequest, "mail_not_configured", "Email is not configured on this site.")
		return
	}
	if _, err := netmail.ParseAddress(newEmail); err != nil {
		apiAuthError(c, http.StatusBadRequest, "email_invalid", "Enter a valid email address.")
		return
	}
	if newEmail == strings.ToLower(strings.TrimSpace(user.Email)) {
		apiAuthError(c, http.StatusBadRequest, "email_same", "That is already your current email address.")
		return
	}
	if existing, err := s.users.ByEmail(ctx, newEmail); err == nil && existing != nil {
		apiAuthError(c, http.StatusBadRequest, "email_taken", "That email address is already in use.")
		return
	} else if err != nil && !store.IsNotFound(err) {
		s.logger.Error("security mail: check email availability failed", "error", err)
		apiAuthError(c, http.StatusBadRequest, "email_invalid", "Enter a valid email address.")
		return
	}
	if !s.sendEmailChangeConfirmation(c, user, newEmail) {
		apiAuthError(c, http.StatusBadRequest, "mail_not_configured", "Email is not configured on this site.")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionUserEmailChangeRequest,
		TargetType: "user",
		TargetID:   store.Ptr(user.ID),
		Detail:     map[string]any{"email": newEmail},
	})
	c.JSON(http.StatusOK, gin.H{"sent": true})
}

// spaResendVerificationSubmit 重发邮箱验证邮件（POST /api/v1/settings/verify-email，登录用户）。
// 语言跟随用户自己的设置（sendEmailVerification → userLocalizer），审计行与 SSR 完全相同。
func (s *Server) spaResendVerificationSubmit(c *gin.Context) {
	user, ok := s.spaProfileSessionOnly(c)
	if !ok {
		return
	}
	if !s.sendEmailVerification(c, user) {
		apiAuthError(c, http.StatusBadRequest, "mail_not_configured", "Email is not configured on this site.")
		return
	}
	s.audit(c.Request.Context(), store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionUserEmailVerifyRequest,
		TargetType: "user",
		TargetID:   store.Ptr(user.ID),
	})
	c.JSON(http.StatusOK, gin.H{"sent": true})
}

// ── 共享判定（JSON 传输专用）────────────────────────────────────────────────

// actionTokenErrorCode 把令牌消费错误映射到稳定英文 code（与 SSR 的 actionTokenErrorKey 同源规则）。
// 前端按 code 映射本地化文案，绝不解析英文 message（DESIGN.md §8.3）。
func actionTokenErrorCode(err error) string {
	switch {
	case errors.Is(err, store.ErrActionTokenExpired):
		return "token_expired"
	case errors.Is(err, store.ErrActionTokenUsed):
		return "token_used"
	default:
		return "token_invalid"
	}
}

// passwordPolicyErrorCode 校验新密码强度并返回稳定英文 code；通过时返回空串。
// 判定规则与 SSR 的 validateNewPassword 一致（同一批 auth 常量），只是把本地化文案换成 code，
// 供 JSON 传输使用；两处都以 auth.MinPasswordLength / MaxPasswordLength / ValidatePasswordPolicy 为准。
func passwordPolicyErrorCode(password string) string {
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

// passwordPolicyErrorMessage 返回密码强度 code 对应的稳定英文兜底文案。
// 与 registrationErrorMessage 同一约定：code 稳定、message 仅作英文兜底，前端按 code 本地化。
func passwordPolicyErrorMessage(code string) string {
	switch code {
	case "password_required":
		return "Enter a password."
	case "password_too_short":
		return "The password is too short."
	case "password_too_long":
		return "The password is too long."
	case "password_too_common":
		return "The password is too common."
	default:
		return "The request is invalid."
	}
}
