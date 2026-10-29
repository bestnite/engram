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

// This file is the same-origin JSON transport layer for account security and mail flows in the SPA.
//
// It only adds "transport": token issuance/consumption, one-shot semantics, anonymous rate limiting, key invalidation after a password change and audit rows all reuse the existing
// service logic. The navigable read pages (/forgot-password, /reset-password, /settings/email) and the one-click
// GET links in emails (/verify-email, /confirm-email-change) only return the app shell; pages and token consumption all go through the
// JSON endpoints here; these pages therefore have no SSR fallback.

// registerAccountRoutes mounts the SPA JSON endpoints and app-shell entry points for account security and mail flows.
// Skipped when the token service is not wired up, matching the precondition of registerSecurityMailRoutes.
func (s *Server) registerAccountRoutes(router *gin.Engine) {
	if s.tokens == nil {
		return
	}
	// Pre-login flows: there is no session to bind CSRF to, so use the double-submit cookie (same as the SSR /forgot-password and /reset-password).
	router.POST("/api/v1/auth/forgot-password", auth.PreSessionCSRFMiddleware(), s.forgotPasswordSubmit)
	router.POST("/api/v1/auth/reset-password", auth.PreSessionCSRFMiddleware(), s.resetPasswordSubmit)
	router.POST("/api/v1/auth/verify-email", auth.PreSessionCSRFMiddleware(), s.verifyEmailSubmit)
	router.POST("/api/v1/auth/confirm-email-change", auth.PreSessionCSRFMiddleware(), s.confirmEmailChangeSubmit)
	if s.sessions != nil {
		router.GET("/api/v1/settings/email", s.emailSettingsGet)
		router.POST("/api/v1/settings/email", s.sessions.CSRFMiddleware(), s.emailChangeSubmit)
		router.POST("/api/v1/settings/verify-email", s.sessions.CSRFMiddleware(), s.resendVerificationSubmit)
	}
}

// ── App shell entry points (pages only return the app shell; reads and writes go through JSON endpoints) ──

// forgotPasswordRoute serves GET /forgot-password: issues the pre-session double-submit cookie and returns the app shell,
// and the client router renders the "request reset" page; the request-reset protocol goes through POST /api/v1/auth/forgot-password (the same service logic).
// The "SMTP not configured" notice is driven by the response's mail_ready field and is no longer rendered by the server.
func (s *Server) forgotPasswordRoute(c *gin.Context) {
	auth.EnsureDoubleSubmitToken(c, s.secureCookies())
	s.shell.ServeIndex(c)
}

// resetPasswordRoute serves GET /reset-password: issues the pre-session double-submit cookie and returns the app shell,
// and the client router reads token from the URL to render the "set new password" page; submission goes through POST /api/v1/auth/reset-password.
// Whether token is missing or invalid is decided by the JSON endpoint.
func (s *Server) resetPasswordRoute(c *gin.Context) {
	auth.EnsureDoubleSubmitToken(c, s.secureCookies())
	s.shell.ServeIndex(c)
}

// emailChangeRoute serves GET /settings/email: returns the app shell, and the client router renders the email-change page;
// reads and submits go through /api/v1/settings/email (the same service logic). Authorization decisions match the pre-migration ones item by item — this is a signed-in user's
// own page, so anyone signed out is always redirected to the login page and requireUser runs before the shell cutover; the cutover never loosens it.
func (s *Server) emailChangeRoute(c *gin.Context) {
	if _, ok := s.requireUser(c); !ok {
		return
	}
	s.shell.ServeIndex(c)
}

// verifyEmailShell is the canonical entry point for email verification (GET /verify-email). The one-click link in emails points here,
// and the SPA reads ?token= and consumes it via POST /api/v1/auth/verify-email (one-shot, expiring).
// Like /login it first issues the pre-session double-submit cookie, then returns the app shell; this only returns the shell and consumes no token.
func (s *Server) verifyEmailShell(c *gin.Context) {
	auth.EnsureDoubleSubmitToken(c, s.secureCookies())
	s.shell.ServeIndex(c)
}

// confirmEmailChangeShell is the canonical entry point for email-change confirmation (GET /confirm-email-change).
// Isomorphic to /verify-email: issues the double-submit cookie and returns the app shell; the confirmation protocol goes through
// POST /api/v1/auth/confirm-email-change. This only returns the shell and consumes no token.
func (s *Server) confirmEmailChangeShell(c *gin.Context) {
	auth.EnsureDoubleSubmitToken(c, s.secureCookies())
	s.shell.ServeIndex(c)
}

// ── JSON endpoints (pre-session) ──

// apiForgotPasswordRequest is the request body for POST /api/v1/auth/forgot-password.
type apiForgotPasswordRequest struct {
	Email string `json:"email"`
}

// forgotPasswordSubmit handles the SPA's reset request (POST /api/v1/auth/forgot-password).
//
// The anonymous rate limit (IP + target mailbox, 5 times each / 15 minutes) runs before the DB lookup and
// enqueueing to the outbox; the same response shape is returned whether or not the mailbox exists, avoiding account enumeration. mail_ready in the response only reflects the site-level
// "is mail configured", unrelated to whether the account exists; the frontend uses it to render a "mail is not configured" notice when unconfigured instead of
// falsely reporting "sent". No token is returned here and the mailbox is not echoed back.
func (s *Server) forgotPasswordSubmit(c *gin.Context) {
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

// apiResetPasswordRequest is the request body for POST /api/v1/auth/reset-password.
type apiResetPasswordRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// resetPasswordSubmit consumes a one-shot reset token and sets the new password (POST /api/v1/auth/reset-password).
//
// Tokens are issued only by the server and only their digest is stored, and consumption is a conditional update (one-shot); when the password is non-compliant the token has already been consumed — this is deliberate,
// consistent with SSR: a one-shot token is void once used, the user simply goes through "forgot password" again, and no window for repeated attempts is left open.
// Key invalidation on password change is handled by SetPasswordFromResetTx; the audit rows are exactly the same as SSR.
func (s *Server) resetPasswordSubmit(c *gin.Context) {
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
	s.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(tok.UserID),
		Action:     store.ActionUserPasswordResetComplete,
		TargetType: "user",
		TargetID:   store.Ptr(tok.UserID),
	})
	c.JSON(http.StatusOK, gin.H{"reset": true})
}

// apiTokenOnlyRequest is a request body carrying only a single token (verify email, confirm email change).
type apiTokenOnlyRequest struct {
	Token string `json:"token"`
}

// verifyEmailSubmit consumes the email verification token and marks the email verified (POST /api/v1/auth/verify-email).
// Token semantics are exactly the same as SSR's verifyEmail: one-shot, expiring; success writes an audit row, failure returns a stable code.
func (s *Server) verifyEmailSubmit(c *gin.Context) {
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
	s.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(tok.UserID),
		Action:     store.ActionUserEmailVerified,
		TargetType: "user",
		TargetID:   store.Ptr(tok.UserID),
	})
	c.JSON(http.StatusOK, gin.H{"verified": true})
}

// confirmEmailChangeSubmit consumes the email-change token and actually updates the email (POST /api/v1/auth/confirm-email-change).
// The token Payload holds the new mailbox pending confirmation; a successful confirmation writes an audit row, and a conflicting new mailbox returns 409 (same as SSR).
func (s *Server) confirmEmailChangeSubmit(c *gin.Context) {
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
	s.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(tok.UserID),
		Action:     store.ActionUserEmailChanged,
		TargetType: "user",
		TargetID:   store.Ptr(tok.UserID),
		Detail:     map[string]any{"email": newEmail},
	})
	c.JSON(http.StatusOK, gin.H{"changed": true})
}

// ── JSON endpoints (session) ──

// emailSettingsGet returns the current user's mailbox, verification state and whether site mail is available (GET /api/v1/settings/email).
// Accepts browser sessions only; when the session is missing, profileSessionOnly returns 401. Never returns any other user's data.
func (s *Server) emailSettingsGet(c *gin.Context) {
	user, ok := s.profileSessionOnly(c)
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

// apiEmailChangeRequest is the request body for POST /api/v1/settings/email.
type apiEmailChangeRequest struct {
	Email string `json:"email"`
}

// emailChangeSubmit submits an email-change request (POST /api/v1/settings/email): sends a confirmation mail to the new address and does not touch the DB before confirmation.
//
// SMTP unconfigured, malformed format, same as the original mailbox and already taken are all rejected with
// no DB write; success only enqueues the confirmation mail and writes an audit row (ActionUserEmailChangeRequest), and the mailbox in the DB stays unchanged until the confirmation link is clicked.
func (s *Server) emailChangeSubmit(c *gin.Context) {
	user, ok := s.profileSessionOnly(c)
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
	s.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionUserEmailChangeRequest,
		TargetType: "user",
		TargetID:   store.Ptr(user.ID),
		Detail:     map[string]any{"email": newEmail},
	})
	c.JSON(http.StatusOK, gin.H{"sent": true})
}

// resendVerificationSubmit resends the email verification mail (POST /api/v1/settings/verify-email, signed-in users).
// The language follows the user's own settings (sendEmailVerification → userLocalizer); the audit rows are exactly the same as SSR.
func (s *Server) resendVerificationSubmit(c *gin.Context) {
	user, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	if !s.sendEmailVerification(c, user) {
		apiAuthError(c, http.StatusBadRequest, "mail_not_configured", "Email is not configured on this site.")
		return
	}
	s.auditor.Record(c.Request.Context(), store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionUserEmailVerifyRequest,
		TargetType: "user",
		TargetID:   store.Ptr(user.ID),
	})
	c.JSON(http.StatusOK, gin.H{"sent": true})
}

// ── Shared decisions (JSON transport only) ──

// actionTokenErrorCode maps token consumption errors to stable English codes.
// The frontend maps localized copy by code and never parses the English message.
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

// passwordPolicyErrorCode validates new-password strength and returns a stable English code; returns an empty string when it passes.
// The decision follows the auth constants (auth.MinPasswordLength / MaxPasswordLength / ValidatePasswordPolicy),
// merely replacing localized copy with a stable code for use by the JSON transport.
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

// passwordPolicyErrorMessage returns the stable English fallback copy for a password-strength code.
// Same convention as registrationErrorMessage: codes are stable, the message is only an English fallback, and the frontend localizes by code.
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
