package web

import (
	"net/url"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the password reset flow (server side): token issuance and delivery, plus the app-shell entries for three pages.
//
// Security points:
//   - the plaintext token appears only in the mail link; never stored, never logged (the log records only user_id and the outcome);
//   - the token table stores only a digest, one-time (conditional update), with a 1-hour expiry;
//   - reset requests always return the same result regardless of whether the email exists, avoiding account enumeration;
//   - with SMTP unconfigured the response renders mail_ready=false — never silent.
//
// All page read/write protocols go through same-origin JSON endpoints (account.go); this file keeps only the page shell and token issuance.

// registerSecurityMailRoutes mounts the password reset, email verification and email change confirmation routes.
// Skipped when the dependency is not wired up, so M0-stage tests can still construct a Server.
func (s *Server) registerSecurityMailRoutes(router *gin.Engine) {
	if s.tokens == nil {
		return
	}
	// The three navigable pre-login pages just return the SPA app shell; the protocols go through
	// /api/v1/auth/{forgot-password,reset-password} (the same service logic as the SPA).
	router.GET("/forgot-password", s.forgotPasswordRoute)
	router.GET("/reset-password", s.resetPasswordRoute)
	// The one-click GET link in the mail lands on the canonical path and returns the app shell; the token is consumed by the SPA through
	// /api/v1/auth/{verify-email,confirm-email-change} (one-time, with expiry).
	// Pre-session write requests are checked by PreSessionCSRFMiddleware against the double-submit cookie and the mirrored token.
	router.GET("/verify-email", s.verifyEmailShell)
	router.GET("/confirm-email-change", s.confirmEmailChangeShell)
	// The change-email page belongs to the signed-in user: when not signed in it redirects to the login page first.
	router.GET("/settings/email", s.emailChangeRoute)
	// The SPA's same-origin JSON transport and app-shell entries (account.go).
	s.registerAccountRoutes(router)
}

// The security/transaction flows have no server-side rendering: only same-origin JSON endpoints and the SPA app shell, with every page presented by the frontend router.

// issuePasswordReset issues a password reset token and delivers the mail; any failure only logs in English (never echoed back).
// The log never contains the plaintext token.
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
	msg := s.passwordResetMessage(ctx, loc, u.Email, u.Username, link, securitySiteName(loc), now.Add(auth.PasswordResetTTL))
	s.sendSecurity(ctx, u.ID, msg.To, mail.TypePasswordReset, msg.Subject, msg.TextBody, msg.HTMLBody)
}
