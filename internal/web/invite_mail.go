package web

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is: invite email delivery (type B collaboration/authorization).
//
// Design constraints (applied item by item):
//   - Never send mail synchronously on the request path: always enqueue via Outbox.Enqueue and let the background worker deliver.
//   - A failed send must not fail the triggering operation: creating the invite still succeeds, and an Enqueue error only logs in English and echoes a notice.
//   - With SMTP unconfigured the flow is disabled and gives the reason: the invite page's "send to email" entry renders mail.not_configured,
//     and submission uses the same explanatory copy — never silently do nothing.
//   - Type B is optional: if the recipient is a user of this site, respect their explicit preference for the invite type; otherwise fall back to the default (on).
//   - The one-click unsubscribe header is a later task; this task sets no Message.Headers (kept empty).
//
// Invite storage / validation / acceptance logic is completely unchanged; this only appends the "mail the invite" step after the existing creation flow.

// Notice codes for the invite email echoed back to the admin (turned into language-pack copy by regNotice).
const (
	noticeInviteMailQueued       = "invite_mail_queued"
	noticeInviteMailUnconfigured = "invite_mail_unconfigured"
	noticeInviteMailFailed       = "invite_mail_failed"
	noticeInviteMailOptedOut     = "invite_mail_opted_out"
)

// sendInviteEmail decides, after an invite has been created successfully, whether to mail it to the invitee, and enqueues it when needed.
//
// The return value is the notice code for the page to echo; an empty string means the caller keeps invite_created:
//   - noticeInviteMailQueued: enqueued, waiting for the worker to deliver;
//   - noticeInviteMailUnconfigured: mail requested but SMTP unconfigured (with the mail.not_configured explanation);
//   - noticeInviteMailFailed: enqueue failed (the invite is still created successfully);
//   - noticeInviteMailOptedOut: the recipient has turned this class of mail off (the invite is still created successfully).
//
// Key: whatever is returned, the invite itself is already persisted — no mail problem may fail the triggering operation.
func (s *Server) sendInviteEmail(c *gin.Context, loc *i18n.Localizer, inv *store.Invite) string {
	if inv.Email == nil || strings.TrimSpace(*inv.Email) == "" {
		// With no recipient address there is nothing to mail; keep the existing manual copy-the-link path.
		return ""
	}
	to := strings.TrimSpace(*inv.Email)
	ctx := c.Request.Context()

	// SMTP unconfigured: the entry point already renders the explanation on the page; cover it once more here and never silently drop it.
	if s.mail == nil || !s.mail.Configured() {
		return noticeInviteMailUnconfigured
	}

	// Preference gate: if the recipient is a user of this site, respect their explicit choice for type B (invite); otherwise fall back to the catalog default (on).
	recipient := s.inviteRecipient(ctx, to)
	var userID uint64
	if recipient != nil {
		userID = recipient.ID
	}
	choices, err := store.NewEmailPrefStore(s.db).Choices(ctx, userID)
	if err != nil {
		// A failed preference read does not block: fall back to the default (type B defaults on) and keep trying to enqueue, but never silently.
		s.logger.Error("invite mail: load email preferences failed", "user_id", userID, "error", err)
		choices = map[string]bool{}
	}
	if !mail.ResolveEnabled(choices, mail.TypeInvite) {
		return noticeInviteMailOptedOut
	}

	// Copy language: a known recipient gets their preferred language, otherwise the admin's current request language is used.
	recipientLoc := loc
	if recipient != nil {
		if code := strings.TrimSpace(recipient.Locale); code != "" {
			recipientLoc = s.i18n.Localizer(s.i18n.Pick(code, "", ""))
		}
	}

	link := s.securityAbsoluteURL(c, "/register?invite="+url.QueryEscape(inv.Token))

	// When the recipient is a user of this site, this type B (optional type) mail carries an unsubscribe
	// entry: the token names the single invite type, so the user can turn it off without logging in. Non-users have no preference to turn off, so none is attached.
	unsubURL := ""
	if recipient != nil {
		unsubURL = s.optionalUnsubscribeLink(c, recipient.ID, mail.TypeInvite)
	}
	msg := s.inviteMessage(ctx, recipientLoc, to, link, s.siteName(ctx, recipientLoc), inv.ExpiresAt, unsubURL)
	msg.Headers = mail.UnsubscribeHeaders(mail.TypeInvite, unsubURL)

	if err := s.mail.Enqueue(ctx, msg); err != nil {
		if errors.Is(err, mail.ErrNotConfigured) {
			return noticeInviteMailUnconfigured
		}
		// An enqueue failure is only logged: the invite is already created, and the whole operation must not fail because of it.
		s.logger.Error("invite mail: enqueue failed", "error", err)
		return noticeInviteMailFailed
	}
	return noticeInviteMailQueued
}

// inviteRecipient finds the site user by email; returns nil when it is not a site user (or the lookup fails).
// Email matching is case-insensitive: emails in the users table are normalized to lowercase at signup.
func (s *Server) inviteRecipient(ctx context.Context, email string) *store.User {
	if s.users == nil {
		return nil
	}
	u, err := s.users.ByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if err != nil || u == nil {
		return nil
	}
	return u
}

// inviteMessage assembles the invite email. When unsubURL is non-empty it is rendered into the body footer and the end of the plain-text part —
// the RFC 8058 header is only visible to mail clients, so the body needs a clickable unsubscribe entry too.
func (s *Server) inviteMessage(ctx context.Context, loc *i18n.Localizer, to, link, site string, expiresAt *time.Time, unsubURL string) mail.Message {
	vars := mail.Vars{"site": site, "url": link}
	if expiresAt != nil {
		vars["expires"] = expiresAt.UTC().Format("2006-01-02 15:04 UTC")
	}
	// The expiry is an optional variable: without that line it does not appear (the function decides on the variable, with no extra parameter).
	fbSubject, fbText, _ := mail.DefaultTemplate(mail.TypeInvite, mail.VariantDefault, loc.Tf, vars)
	subject, text, htmlBody := s.renderMail(ctx, loc, mail.TypeInvite, vars, fbSubject, fbText, unsubURL)
	return mail.Message{To: to, Type: string(mail.TypeInvite), Subject: subject, TextBody: text, HTMLBody: htmlBody}
}
