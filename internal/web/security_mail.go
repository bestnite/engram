package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the web-layer part of class-A transactional security mail: the request-derived site root, the recipient's
// localizer, and the trigger points. Composing, gating and enqueueing all go through mail.Mailer.Send, which never
// returns an error, so a failed mail cannot fail a login, a password change or an account change.
//
// Class A mail is not affected by user preferences and carries no unsubscribe header (mail.Mailer decides both from the
// type's class). When SMTP is not configured the flows that depend on mail disable themselves and say so on the page.

// securityMailReady reports whether mail is currently deliverable (SMTP configured and outbox wired up).
// Pages that depend on mail disable themselves and render a "mail is not configured" notice from it, instead of silently doing nothing.
func (s *Server) securityMailReady() bool {
	return s.mailer.Configured()
}

// requestBaseURL is the site root for links in a mail sent while handling c: BASE_URL when configured, otherwise derived
// from the current request (local development and httptest rely on the latter).
func (s *Server) requestBaseURL(c *gin.Context) string {
	if base := strings.TrimRight(strings.TrimSpace(s.baseURL), "/"); base != "" {
		return base
	}
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if proto := c.GetHeader("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	return scheme + "://" + c.Request.Host
}

// absoluteURL builds a clickable absolute URL for a mail sent while handling c, from requestBaseURL.
func (s *Server) absoluteURL(c *gin.Context, path string) string {
	return s.requestBaseURL(c) + path
}

// userLocalizer returns the localizer for the recipient's language: mail copy follows the user's own language setting,
// not that of the admin whose request triggered it. Falls back to the site default when the user has set none.
func (s *Server) userLocalizer(u *store.User) *i18n.Localizer {
	code := ""
	if u != nil {
		code = strings.TrimSpace(u.Locale)
	}
	return s.i18n.Localizer(s.i18n.Pick(code, "", ""))
}

// sendMail sends one mail through the shared mailer and reports whether it was queued.
func (s *Server) sendMail(ctx context.Context, loc *i18n.Localizer, m mail.Mail) bool {
	return s.mailer.Send(ctx, loc, m) == mail.SendQueued
}

// mailTime formats an instant the way every mail shows it.
func mailTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

// loginFingerprint normalizes the client IP and User-Agent into a stable fingerprint (sha256 hex).
// The hash is stored rather than the raw text: enough for deduplication, without piling raw user agents up in the database over time (privacy minimization).
func loginFingerprint(c *gin.Context) string {
	ip := strings.TrimSpace(c.ClientIP())
	ua := strings.TrimSpace(c.GetHeader("User-Agent"))
	sum := sha256.Sum256([]byte(ip + "\n" + ua))
	return hex.EncodeToString(sum[:])
}

// ── trigger-point notifications (each call site needs one line; never returns an error) ────────────────────────────────
//
// Variable names must match the table in internal/mail/vars.go, otherwise the required-variable check stops the send (a
// deliberate direction of failure: better not to send at all than to send a mail with no reset link).

// notifyNewDeviceLogin records the login fingerprint and delivers the new device / new IP login alert when the fingerprint is new.
// No mail is sent when the fingerprint has been seen (or was just recorded by a concurrent request), so the same device is not alerted over and over.
func (s *Server) notifyNewDeviceLogin(c *gin.Context, u *store.User) {
	if s.fingerprints == nil || u == nil {
		return
	}
	ctx := c.Request.Context()
	now := time.Now().UTC()
	known, err := s.fingerprints.Touch(ctx, u.ID, loginFingerprint(c), now)
	if err != nil {
		s.logger.Error("security mail: touch login fingerprint failed", "user_id", u.ID, "error", err)
		return
	}
	if known {
		return
	}
	s.sendMail(ctx, s.userLocalizer(u), mail.Mail{
		Type: mail.TypeNewDeviceLogin, To: u.Email, UserID: u.ID,
		Vars: mail.Vars{"ip": strings.TrimSpace(c.ClientIP()), "time": mailTime(now)},
	})
}

// notifyCredentialChanged delivers the notice after a password/TOTP/recovery-code change.
// kind is the key suffix of mail.notice.credential.* (password / totp_enabled / totp_disabled / recovery_codes).
func (s *Server) notifyCredentialChanged(ctx context.Context, u *store.User, kind string) {
	if u == nil {
		return
	}
	loc := s.userLocalizer(u)
	s.sendMail(ctx, loc, mail.Mail{
		Type: mail.TypeCredentialChanged, To: u.Email, UserID: u.ID,
		Vars: mail.Vars{"what": loc.T("mail.notice.credential." + kind)},
	})
}

// notifyAccountStatus delivers the notice after an account is disabled or deleted.
// status is the key suffix of mail.notice.account.* (disabled / deleted).
func (s *Server) notifyAccountStatus(ctx context.Context, u *store.User, status string) {
	if u == nil {
		return
	}
	loc := s.userLocalizer(u)
	s.sendMail(ctx, loc, mail.Mail{
		Type: mail.TypeAccountStatus, To: u.Email, UserID: u.ID,
		Vars: mail.Vars{"status": loc.T("mail.notice.account." + status)},
	})
}

// sendEmailVerification issues a verification token and delivers the verification mail; returns whether it was enqueued successfully.
// Returns false when SMTP is not configured or issuing the token fails; the caller explains the reason in the page/response and never stays silent.
func (s *Server) sendEmailVerification(c *gin.Context, u *store.User) bool {
	if s.tokens == nil || u == nil || !s.securityMailReady() {
		return false
	}
	ctx := c.Request.Context()
	now := time.Now().UTC()
	token, err := s.tokens.Issue(ctx, u.ID, store.ActionTokenEmailVerify, "", auth.EmailVerifyTTL)
	if err != nil {
		s.logger.Error("security mail: issue email verification token failed", "user_id", u.ID, "error", err)
		return false
	}
	return s.sendMail(ctx, s.userLocalizer(u), mail.Mail{
		Type: mail.TypeEmailVerification, To: u.Email, UserID: u.ID,
		Vars: mail.Vars{
			"url":     s.absoluteURL(c, "/verify-email?token="+url.QueryEscape(token)),
			"expires": mailTime(now.Add(auth.EmailVerifyTTL)),
		},
	})
}

// sendEmailChangeConfirmation issues an email change token for the new address awaiting confirmation and delivers the confirmation mail.
//
// It reuses the email verification type (both are "confirm an email address"), so a template an admin configures for
// email_verification applies to both, with the email-change variant of the built-in body.
func (s *Server) sendEmailChangeConfirmation(c *gin.Context, u *store.User, newEmail string) bool {
	if s.tokens == nil || u == nil || !s.securityMailReady() {
		return false
	}
	ctx := c.Request.Context()
	now := time.Now().UTC()
	token, err := s.tokens.Issue(ctx, u.ID, store.ActionTokenEmailChange, newEmail, auth.EmailChangeTTL)
	if err != nil {
		s.logger.Error("security mail: issue email change token failed", "user_id", u.ID, "error", err)
		return false
	}
	return s.sendMail(ctx, s.userLocalizer(u), mail.Mail{
		Type: mail.TypeEmailVerification, Variant: mail.VariantEmailChange, To: newEmail, UserID: u.ID,
		Vars: mail.Vars{
			"url":     s.absoluteURL(c, "/confirm-email-change?token="+url.QueryEscape(token)),
			"expires": mailTime(now.Add(auth.EmailChangeTTL)),
		},
	})
}
