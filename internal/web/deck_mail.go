package web

import (
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
	loc := s.userLocalizer(target)
	// On revocation newRole is empty: the built-in body decides from the role variable that the mail says "access was
	// revoked", not "permission was changed to empty".
	s.sendMail(c.Request.Context(), loc, mail.Mail{
		Type: deckMailType(previousRole, newRole), To: target.Email, UserID: target.ID, BaseURL: s.requestBaseURL(c),
		Vars: mail.Vars{
			"inviter": actor.Username, "actor": actor.Username, "deck": deck.Name,
			"role": roleLabelFor(loc, newRole), "url": s.absoluteURL(c, "/decks/"+deck.PublicID),
		},
	})
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
