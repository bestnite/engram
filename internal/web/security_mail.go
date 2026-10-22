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

// This file is the web-layer common part of (class A transactional security mail): the delivery helper, link assembly,
// the copy assembly for the five mail types, and the notify functions the trigger points reuse.
//
// Three behavioral constraints are met in one place here:
//   - never send mail synchronously on the request path: always enqueue via SecurityNotifier.Send → Outbox.Enqueue;
//   - a failed send never fails the triggering action: no notify function returns an error, failures only log in English;
//   - when SMTP is not configured the flow is disabled and states why: the page renders mail.not_configured (see the page handlers).
//
// Class A mail is not affected by user preferences and carries no unsubscribe header (SecurityNotifier never writes Headers).

// securityMailReady reports whether class A mail is currently deliverable (SMTP configured and outbox wired up).
// Pages that depend on mail disable themselves and render mail.not_configured from it, instead of silently doing nothing.
func (s *Server) securityMailReady() bool {
	return s.securityMail != nil && s.securityMail.Configured()
}

// securityAbsoluteURL builds the clickable absolute URL for a mail: BASE_URL is used when configured,
// otherwise it is derived from the current request (local development and httptest rely on the latter).
func (s *Server) securityAbsoluteURL(c *gin.Context, path string) string {
	if base := strings.TrimRight(strings.TrimSpace(s.baseURL), "/"); base != "" {
		return base + path
	}
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if proto := c.GetHeader("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	return scheme + "://" + c.Request.Host + path
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

// sendSecurity is the single delivery exit for class A mail; it deliberately returns no error:
// triggering actions such as a successful login or password change must never fail because enqueueing failed.
func (s *Server) sendSecurity(ctx context.Context, userID uint64, to string, t mail.Type, subject, textBody, htmlBody string) bool {
	if s.securityMail == nil {
		return false
	}
	return s.securityMail.Send(ctx, userID, to, t, subject, textBody, htmlBody)
}

// loginFingerprint normalizes the client IP and User-Agent into a stable fingerprint (sha256 hex).
// The hash is stored rather than the raw text: enough for deduplication, without piling raw user agents up in the database over time (privacy minimization).
func loginFingerprint(c *gin.Context) string {
	ip := strings.TrimSpace(c.ClientIP())
	ua := strings.TrimSpace(c.GetHeader("User-Agent"))
	sum := sha256.Sum256([]byte(ip + "\n" + ua))
	return hex.EncodeToString(sum[:])
}

// ── copy assembly for the five mail types (the default body is plain text only; an HTML part is produced only when an admin configured a template) ──────────
//
// Each assembly function does three things: provide the template variables, provide the built-in body (today's behavior), and hand it to renderMail to render.
// Variable names must match the table in internal/mail/vars.go, otherwise the required-variable check stops the send (this is a deliberate
// direction of failure: better not to send at all than to send a mail with no reset link).

// passwordResetMessage assembles the password reset mail. The plaintext link appears only here and never reaches the database or the logs.
func (s *Server) passwordResetMessage(ctx context.Context, loc *i18n.Localizer, to, link, site string, expires time.Time) mail.Message {
	expiresAt := expires.UTC().Format("2006-01-02 15:04 UTC")
	vars := mail.Vars{"site": site, "url": link, "expires": expiresAt}
	fbSubject, fbText, _ := mail.DefaultTemplate(mail.TypePasswordReset, mail.VariantDefault, loc.Tf, vars)
	subject, text, htmlBody := s.renderMail(ctx, loc, mail.TypePasswordReset, vars, fbSubject, fbText, "")
	return mail.Message{To: to, Type: string(mail.TypePasswordReset), Subject: subject, TextBody: text, HTMLBody: htmlBody}
}

// emailVerificationMessage assembles the email verification mail.
func (s *Server) emailVerificationMessage(ctx context.Context, loc *i18n.Localizer, to, link, site string, expires time.Time) mail.Message {
	expiresAt := expires.UTC().Format("2006-01-02 15:04 UTC")
	vars := mail.Vars{"site": site, "url": link, "expires": expiresAt}
	fbSubject, fbText, _ := mail.DefaultTemplate(mail.TypeEmailVerification, mail.VariantDefault, loc.Tf, vars)
	subject, text, htmlBody := s.renderMail(ctx, loc, mail.TypeEmailVerification, vars, fbSubject, fbText, "")
	return mail.Message{To: to, Type: string(mail.TypeEmailVerification), Subject: subject, TextBody: text, HTMLBody: htmlBody}
}

// emailChangeMessage assembles the email change confirmation mail; it goes to the new address awaiting confirmation.
//
// It reuses the email verification type (both are "confirm an email address"), so a template an admin configures for email_verification
// applies to both kinds of mail, and the set of variables is the same one (site/url/expires).
func (s *Server) emailChangeMessage(ctx context.Context, loc *i18n.Localizer, to, link, site string, expires time.Time) mail.Message {
	expiresAt := expires.UTC().Format("2006-01-02 15:04 UTC")
	vars := mail.Vars{"site": site, "url": link, "expires": expiresAt}
	fbSubject, fbText, _ := mail.DefaultTemplate(mail.TypeEmailVerification, mail.VariantEmailChange, loc.Tf, vars)
	subject, text, htmlBody := s.renderMail(ctx, loc, mail.TypeEmailVerification, vars, fbSubject, fbText, "")
	return mail.Message{To: to, Type: string(mail.TypeEmailVerification), Subject: subject, TextBody: text, HTMLBody: htmlBody}
}

// newDeviceLoginMessage assembles the new device / new IP login alert.
func (s *Server) newDeviceLoginMessage(ctx context.Context, loc *i18n.Localizer, to, site, ip, userAgent string, at time.Time) mail.Message {
	atText := at.UTC().Format("2006-01-02 15:04 UTC")
	vars := mail.Vars{"site": site, "ip": ip, "time": atText}
	fbSubject, fbText, _ := mail.DefaultTemplate(mail.TypeNewDeviceLogin, mail.VariantDefault, loc.Tf, vars)
	subject, text, htmlBody := s.renderMail(ctx, loc, mail.TypeNewDeviceLogin, vars, fbSubject, fbText, "")
	return mail.Message{To: to, Type: string(mail.TypeNewDeviceLogin), Subject: subject, TextBody: text, HTMLBody: htmlBody}
}

// credentialChangedMessage assembles the credential change notice; what is the already-localized phrase for "what changed".
func (s *Server) credentialChangedMessage(ctx context.Context, loc *i18n.Localizer, to, site, what string) mail.Message {
	vars := mail.Vars{"site": site, "what": what}
	fbSubject, fbText, _ := mail.DefaultTemplate(mail.TypeCredentialChanged, mail.VariantDefault, loc.Tf, vars)
	subject, text, htmlBody := s.renderMail(ctx, loc, mail.TypeCredentialChanged, vars, fbSubject, fbText, "")
	return mail.Message{To: to, Type: string(mail.TypeCredentialChanged), Subject: subject, TextBody: text, HTMLBody: htmlBody}
}

// accountStatusMessage assembles the account status change notice; status is the already-localized status phrase.
func (s *Server) accountStatusMessage(ctx context.Context, loc *i18n.Localizer, to, site, status string) mail.Message {
	vars := mail.Vars{"site": site, "status": status}
	fbSubject, fbText, _ := mail.DefaultTemplate(mail.TypeAccountStatus, mail.VariantDefault, loc.Tf, vars)
	subject, text, htmlBody := s.renderMail(ctx, loc, mail.TypeAccountStatus, vars, fbSubject, fbText, "")
	return mail.Message{To: to, Type: string(mail.TypeAccountStatus), Subject: subject, TextBody: text, HTMLBody: htmlBody}
}

// ── trigger-point notifications (each call site needs one line; never returns an error) ────────────────────────────────

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
	loc := s.userLocalizer(u)
	msg := s.newDeviceLoginMessage(ctx, loc, u.Email, securitySiteName(loc), strings.TrimSpace(c.ClientIP()),
		strings.TrimSpace(c.GetHeader("User-Agent")), now)
	s.sendSecurity(ctx, u.ID, msg.To, mail.TypeNewDeviceLogin, msg.Subject, msg.TextBody, msg.HTMLBody)
}

// notifyCredentialChanged delivers the notice after a password/TOTP/recovery-code change.
// kind is the key suffix of mail.notice.credential.* (password / totp_enabled / totp_disabled / recovery_codes).
func (s *Server) notifyCredentialChanged(ctx context.Context, u *store.User, kind string) {
	if u == nil {
		return
	}
	loc := s.userLocalizer(u)
	msg := s.credentialChangedMessage(ctx, loc, u.Email, securitySiteName(loc), loc.T("mail.notice.credential."+kind))
	s.sendSecurity(ctx, u.ID, msg.To, mail.TypeCredentialChanged, msg.Subject, msg.TextBody, msg.HTMLBody)
}

// notifyAccountStatus delivers the notice after an account is disabled or deleted.
// status is the key suffix of mail.notice.account.* (disabled / deleted).
func (s *Server) notifyAccountStatus(ctx context.Context, u *store.User, status string) {
	if u == nil {
		return
	}
	loc := s.userLocalizer(u)
	msg := s.accountStatusMessage(ctx, loc, u.Email, securitySiteName(loc), loc.T("mail.notice.account."+status))
	s.sendSecurity(ctx, u.ID, msg.To, mail.TypeAccountStatus, msg.Subject, msg.TextBody, msg.HTMLBody)
}

// securitySiteName takes the site brand name as {site} in mails.
func securitySiteName(loc *i18n.Localizer) string { return loc.T("app.name") }

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
	loc := s.userLocalizer(u)
	link := s.securityAbsoluteURL(c, "/verify-email?token="+url.QueryEscape(token))
	msg := s.emailVerificationMessage(ctx, loc, u.Email, link, securitySiteName(loc), now.Add(auth.EmailVerifyTTL))
	return s.sendSecurity(ctx, u.ID, msg.To, mail.TypeEmailVerification, msg.Subject, msg.TextBody, msg.HTMLBody)
}

// sendEmailChangeConfirmation issues an email change token for the new address awaiting confirmation and delivers the confirmation mail.
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
	loc := s.userLocalizer(u)
	link := s.securityAbsoluteURL(c, "/confirm-email-change?token="+url.QueryEscape(token))
	msg := s.emailChangeMessage(ctx, loc, newEmail, link, securitySiteName(loc), now.Add(auth.EmailChangeTTL))
	return s.sendSecurity(ctx, u.ID, msg.To, mail.TypeEmailVerification, msg.Subject, msg.TextBody, msg.HTMLBody)
}
