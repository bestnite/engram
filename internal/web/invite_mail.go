package web

import (
	"context"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the invite email delivery path (type B: collaboration and authorization).
//
// Design constraints, applied one by one:
//   - Never send mail synchronously on the request path: always enqueue via Outbox.Enqueue and let the background worker deliver.
//   - A failed send must not fail the triggering operation: creating the invite still succeeds, and an Enqueue error only logs in English and echoes a notice.
//   - With SMTP unconfigured the flow is disabled and gives the reason: the invite page's "send to email" entry renders a "mail is not configured" notice,
//     and submission uses the same explanatory copy — never silently do nothing.
//   - Type B is optional: if the recipient is a user of this site, respect their explicit preference for the invite type; otherwise fall back to the default (on).
//   - A recipient who is a user of this site gets the one-click unsubscribe link; an outside address has no preference to turn off.
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
//   - noticeInviteMailUnconfigured: mail requested but SMTP unconfigured (with the "mail is not configured" explanation);
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

	// The recipient's own preference and unsubscribe link apply only when the address belongs to a user of this site; an
	// outside address gets the type's default (on) and no unsubscribe link. A known recipient also gets their own language.
	m := mail.Mail{Type: mail.TypeInvite, To: to, BaseURL: s.requestBaseURL(c), Vars: mail.Vars{
		"url": s.absoluteURL(c, "/register?invite="+url.QueryEscape(inv.Token)),
	}}
	// The expiry is an optional variable: without it the built-in body leaves that line out.
	if inv.ExpiresAt != nil {
		m.Vars["expires"] = mailTime(*inv.ExpiresAt)
	}
	recipientLoc := loc
	if recipient := s.inviteRecipient(ctx, to); recipient != nil {
		m.UserID = recipient.ID
		if code := strings.TrimSpace(recipient.Locale); code != "" {
			recipientLoc = s.i18n.Localizer(s.i18n.Pick(code, "", ""))
		}
	}

	// Whatever happens to the mail, the invite itself is already persisted.
	switch s.mailer.Send(ctx, recipientLoc, m) {
	case mail.SendQueued:
		return noticeInviteMailQueued
	case mail.SendNotConfigured:
		return noticeInviteMailUnconfigured
	case mail.SendOptedOut:
		return noticeInviteMailOptedOut
	default:
		return noticeInviteMailFailed
	}
}

// inviteRecipient finds the site user by email; returns nil when it is not a site user (or the lookup fails).
// Email matching is case-insensitive: UserStore.ByEmail compares store.NormalizeEmail forms.
func (s *Server) inviteRecipient(ctx context.Context, email string) *store.User {
	if s.users == nil {
		return nil
	}
	u, err := s.users.ByEmail(ctx, email)
	if err != nil || u == nil {
		return nil
	}
	return u
}
