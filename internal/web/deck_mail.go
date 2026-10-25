package web

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// This file covers the two kinds of class B collaboration mail related to deck sharing:
//
//	deck_shared              —— someone shared a deck with you
//	deck_permission_changed  —— your permission on a deck was changed or revoked
//
// Three rules: **never send to yourself** (the actor does not need to receive "I shared it with you"), respect the recipient's preferences, and any failure
// is only logged — the sharing itself must never fail because of a mail failure.

// deckMailType decides which kind to send for this change: going from "no permission" to "has permission" is a share, everything else is a change
// (including revocation, where newRole is an empty string).
func deckMailType(previousRole, newRole string) mail.Type {
	if previousRole == "" && newRole != "" {
		return mail.TypeDeckShared
	}
	return mail.TypeDeckPermissionChanged
}

// notifyDeckGrantChange notifies the grantee after a grant is created, changed or revoked.
//
// It deliberately returns no error: the call site is the sharing write path, and notifying is only a side effect.
func (s *Server) notifyDeckGrantChange(c *gin.Context, deck *store.Deck, actor, target *store.User, previousRole, newRole string) {
	if s == nil || deck == nil || actor == nil || target == nil {
		return
	}
	if target.ID == actor.ID {
		// Changing your own permission (including the owner acting on themselves) has no notification value.
		return
	}
	if s.mail == nil || !s.mail.Configured() {
		// When SMTP is unconfigured a note is enough: the sharing operation itself already succeeded, and the user does not need to see "mail not sent" here.
		s.logger.Info("deck mail: smtp is not configured; notification skipped", "deck_id", deck.ID)
		return
	}
	ctx := c.Request.Context()
	choices, err := store.NewEmailPrefStore(s.db).Choices(ctx, target.ID)
	if err != nil {
		s.logger.Error("deck mail: read recipient preferences failed", "user_id", target.ID, "error", err)
		return
	}
	typ := deckMailType(previousRole, newRole)
	if !mail.ResolveEnabled(choices, typ) {
		return
	}
	loc := s.userLocalizer(target)
	link := s.securityAbsoluteURL(c, "/decks/"+deck.PublicID)
	unsubURL := s.optionalUnsubscribeLink(c, target.ID, typ)
	msg := s.deckGrantMessage(ctx, loc, target.Email, typ, actor.Username, deck.Name, newRole, link, unsubURL)
	if err := s.mail.Enqueue(ctx, msg); err != nil {
		s.logger.Error("deck mail: enqueue failed", "mail_type", string(typ), "user_id", target.ID, "error", err)
	}
}

// deckGrantMessage assembles the share/permission-change mail. The built-in body is Markdown and renders to HTML; a custom template replaces it.
func (s *Server) deckGrantMessage(ctx context.Context, loc *i18n.Localizer, to string, typ mail.Type,
	inviter, deck, role, link, unsubURL string) mail.Message {
	site := securitySiteName(loc)
	roleLabel := roleLabelFor(loc, role)
	// On revocation role is empty: the mail must say "access was revoked", not "permission was changed to empty". That decision now
	// lives in mail.DefaultTemplate (which decides by the role variable), so it is no longer recomputed here.

	vars := mail.Vars{
		"site": site, "inviter": inviter, "actor": inviter,
		"deck": deck, "role": roleLabel, "url": link,
	}
	if unsubURL != "" {
		vars["unsubscribe_url"] = unsubURL
	}

	// The built-in body comes from mail.DefaultTemplate: the "default" pre-filled on the admin page and what is sent here are the same text.
	// On revocation roleLabel is empty, and that branch is decided by the function from the variable itself (no separate parameter).
	fallbackSubject, fallbackBodyMD, _ := mail.DefaultTemplate(typ, mail.VariantDefault, loc.Tf, vars)

	footerNote, unsubLabel := loc.T("mail.footer.automated"), ""
	if unsubURL != "" {
		footerNote = loc.T("mail.footer.note")
		unsubLabel = loc.T("mail.footer.unsubscribe")
	}
	tpl := mail.Resolve(s.mailTemplateLookup(), typ, loc.Locale(), s.siteDefaultLocale(ctx))
	out := mail.RenderOrFallback(mail.RenderInput{
		Type:             typ,
		Site:             site,
		Lang:             loc.Locale(),
		Vars:             vars,
		Subject:          tpl.Subject,
		BodyMD:           tpl.Body,
		FallbackSubject:  fallbackSubject,
		FallbackBodyMD:   fallbackBodyMD,
		UnsubscribeURL:   unsubURL,
		FooterNote:       footerNote,
		UnsubscribeLabel: unsubLabel,
	})
	msg := mail.Message{To: to, Type: string(typ), Subject: out.Subject, TextBody: out.Text, HTMLBody: out.HTML}
	msg.Headers = mail.UnsubscribeHeaders(typ, unsubURL)
	return msg
}

// roleLabelFor translates identifiers like reader/editor into the recipient's language; unknown values are returned as-is (never leaking an empty string).
func roleLabelFor(loc *i18n.Localizer, role string) string {
	switch strings.TrimSpace(role) {
	case store.RoleReader:
		return loc.T("mail.role.reader")
	case store.RoleEditor:
		return loc.T("mail.role.editor")
	default:
		return strings.TrimSpace(role)
	}
}

// notifyDeckRevoke notifies the revoked user after a grant is revoked; if the revoked user can no longer be found it is skipped silently.
func (s *Server) notifyDeckRevoke(c *gin.Context, deck *store.Deck, actor *store.User, targetID uint64, previousRole string) {
	if s == nil || s.users == nil || deck == nil || actor == nil || targetID == 0 {
		return
	}
	target, err := s.users.ByID(c.Request.Context(), targetID)
	if err != nil || target == nil {
		s.logger.Error("deck mail: load revoked user failed", "user_id", targetID, "error", err)
		return
	}
	s.notifyDeckGrantChange(c, deck, actor, target, previousRole, "")
}
