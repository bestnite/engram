package web

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
)

type sharingRequest struct {
	Username string `json:"username"`
	UserID   string `json:"user_id"`
	Role     string `json:"role"`
}
type grant struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

func (s *Server) sharingGet(c *gin.Context) {
	user, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	deckID, ok := s.deckIDParam(c)
	if !ok {
		shareError(c, http.StatusNotFound, "not_found")
		return
	}
	deck, ok := s.loadDeckForRole(c, user, deckID, store.RoleOwner)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	grants, err := s.grants.ListByDeck(ctx, deck.ID)
	if err != nil {
		s.logger.Error("list SPA deck grants failed", "deck_id", deck.ID, "error", err)
		shareError(c, 500, "internal_error")
		return
	}
	grantRows := make([]grant, 0, len(grants))
	for _, g := range grants {
		if g.UserID == deck.OwnerUserID {
			continue
		}
		u, err := s.users.ByID(ctx, g.UserID)
		if err != nil || u == nil {
			// The user a grant row points at no longer exists: there is no public id to show, so it is skipped instead of falling back to the numeric primary key.
			continue
		}
		grantRows = append(grantRows, grant{UserID: u.PublicID, Username: u.Username, Role: g.Role})
	}
	// Pending invites get their own column: the owner must be able to tell "already granted" from "invited but not yet accepted" — the latter may be
	// rejected at any time and must not show up in the UI as existing access.
	pending, err := s.shareInvites.ListForDeck(ctx, deck.ID)
	if err != nil {
		s.logger.Error("list SPA deck share invites failed", "deck_id", deck.ID, "error", err)
		shareError(c, 500, "internal_error")
		return
	}
	c.JSON(http.StatusOK, gin.H{"deck_id": deck.PublicID, "deck_name": deck.Name, "grants": grantRows, "pending_invites": pending})
}

func (s *Server) sharingWrite(c *gin.Context, action string) {
	user, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	deckID, ok := s.deckIDParam(c)
	if !ok {
		shareError(c, 404, "not_found")
		return
	}
	deck, ok := s.loadDeckForRole(c, user, deckID, store.RoleOwner)
	if !ok {
		return
	}
	var req sharingRequest
	if action == "revoke" {
		req.UserID = strings.TrimSpace(c.Param("userID"))
		if req.UserID == "" {
			shareError(c, 400, "invalid_request")
			return
		}
	} else if err := c.ShouldBindJSON(&req); err != nil {
		shareError(c, 400, "invalid_request")
		return
	}
	ctx := c.Request.Context()
	bad := func() { shareError(c, 400, "invalid_request") }
	switch action {
	case "grant":
		role := strings.TrimSpace(req.Role)
		if !store.ValidRole(role) || role == store.RoleOwner {
			bad()
			return
		}
		var target *store.User
		var err error
		switch {
		case strings.TrimSpace(req.UserID) != "":
			target, err = s.users.ByPublicID(ctx, strings.TrimSpace(req.UserID))
		case strings.TrimSpace(req.Username) != "":
			target, err = s.users.ByUsername(ctx, strings.TrimSpace(req.Username))
		default:
			raw := strings.TrimSpace(c.Param("userID"))
			if raw == "" {
				bad()
				return
			}
			target, err = s.users.ByPublicID(ctx, raw)
		}
		if err != nil {
			shareError(c, 404, "not_found")
			return
		}
		if target.ID == deck.OwnerUserID {
			bad()
			return
		}
		existing, err := s.grants.Role(ctx, deck.ID, target.ID)
		if err != nil {
			s.logger.Error("read SPA deck grant failed", "deck_id", deck.ID, "error", err)
			shareError(c, 500, "internal_error")
			return
		}
		if existing != role {
			if existing != "" {
				// People already in: changing their role needs no fresh consent — consent was given at the moment of acceptance.
				if err = s.grants.Grant(ctx, deck.ID, target.ID, role, store.Ptr(user.ID)); err != nil {
					s.logger.Error("grant SPA deck role failed", "deck_id", deck.ID, "error", err)
					shareError(c, 500, "internal_error")
					return
				}
				s.audit(ctx, store.AuditEntry{UserID: store.Ptr(user.ID), Action: store.ActionDeckRoleChange, TargetType: "deck", TargetID: store.Ptr(deck.ID), Detail: map[string]any{"username": target.Username, "user_id": target.PublicID, "role": role, "previous_role": existing}})
				// Notification (class B, unsubscribable). It sits after the audit and returns no error: the notification is only a side effect.
				s.notifyDeckGrantChange(c, deck, user, target, existing, role)
				break
			}
			// Newcomers: **send an invite, do not grant directly** (consent model). deck_grants is only written on acceptance,
			// so "someone forcing a deck on me" cannot happen on this path.
			allowed, policyErr := s.sharePolicy.Allows(ctx, target.ID, user.ID)
			if policyErr != nil {
				s.logger.Error("check recipient share policy failed", "deck_id", deck.ID, "user_id", target.ID, "error", policyErr)
				shareError(c, 500, "internal_error")
				return
			}
			if !allowed {
				// The other side set "do not accept shares" or is not on the allow list: refuse truthfully and let the owner see why.
				shareError(c, 409, "recipient_refuses_shares")
				return
			}
			now := time.Now().UTC()
			if err := s.shareInvites.Invite(ctx, store.DeckShareInvite{
				DeckID: deck.ID, UserID: target.ID, Role: role, InvitedBy: user.ID,
				CreatedAt: now, ExpiresAt: now.Add(ShareInviteTTL),
			}); err != nil {
				s.logger.Error("create deck share invite failed", "deck_id", deck.ID, "error", err)
				shareError(c, 500, "internal_error")
				return
			}
			s.audit(ctx, store.AuditEntry{UserID: store.Ptr(user.ID), Action: store.ActionDeckShareInvite, TargetType: "deck", TargetID: store.Ptr(deck.ID), Detail: map[string]any{"username": target.Username, "user_id": target.PublicID, "role": role}})
			s.notifyDeckGrantChange(c, deck, user, target, "", role)
		}
	case "revoke":
		target, err := s.users.ByPublicID(ctx, strings.TrimSpace(req.UserID))
		if err != nil {
			// An unknown public id looks the same as "this user has no grant to revoke": 404, without leaking existence.
			shareError(c, 404, "not_found")
			return
		}
		id := target.ID
		if id == deck.OwnerUserID {
			bad()
			return
		}
		existing, err := s.grants.Role(ctx, deck.ID, id)
		if err != nil {
			s.logger.Error("read SPA deck grant failed", "deck_id", deck.ID, "error", err)
			shareError(c, 500, "internal_error")
			return
		}
		if err = s.grants.Revoke(ctx, deck.ID, id); err != nil {
			s.logger.Error("revoke SPA deck grant failed", "deck_id", deck.ID, "error", err)
			shareError(c, 500, "internal_error")
			return
		}
		if existing != "" {
			s.audit(ctx, store.AuditEntry{UserID: store.Ptr(user.ID), Action: store.ActionDeckRevoke, TargetType: "deck", TargetID: store.Ptr(deck.ID), Detail: map[string]any{"user_id": target.PublicID, "previous_role": existing}})
			s.notifyDeckRevoke(c, deck, user, id, existing)
			break
		}
		// No active grant, but there may be a pending, not yet accepted invite: in that case "revoke" means cancel the invite.
		if err := s.shareInvites.Delete(ctx, deck.ID, id); err != nil {
			s.logger.Error("cancel deck share invite failed", "deck_id", deck.ID, "user_id", id, "error", err)
			shareError(c, 500, "internal_error")
			return
		}
	default:
		bad()
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
func shareError(c *gin.Context, status int, code string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": api.ErrorMessage(c.Request.Context(), code)}})
}
func (s *Server) sharingGrant(c *gin.Context)  { s.sharingWrite(c, "grant") }
func (s *Server) sharingRevoke(c *gin.Context) { s.sharingWrite(c, "revoke") }
