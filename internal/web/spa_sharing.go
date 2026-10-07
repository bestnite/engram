package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

type spaSharingRequest struct {
	Username   string `json:"username"`
	UserID     uint64 `json:"user_id"`
	Role       string `json:"role"`
	Visibility string `json:"visibility"`
	Link       string `json:"link"`
	Password   string `json:"password"`
	ExpiresAt  string `json:"expires_at"`
}
type spaGrant struct {
	UserID   uint64 `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}
type spaShareLink struct {
	Prefix      string     `json:"prefix"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	HasPassword bool       `json:"has_password"`
	Revoked     bool       `json:"revoked"`
	Expired     bool       `json:"expired"`
	TokenDigest string     `json:"token_digest"`
}

func (s *Server) spaSharingGet(c *gin.Context) {
	user, ok := s.spaProfileSessionOnly(c)
	if !ok {
		return
	}
	deckID, ok := deckIDParam(c)
	if !ok {
		spaShareError(c, http.StatusNotFound, "not_found")
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
		spaShareError(c, 500, "internal_error")
		return
	}
	links, err := s.shareLinks.ListByDeck(ctx, deck.ID)
	if err != nil {
		s.logger.Error("list SPA share links failed", "deck_id", deck.ID, "error", err)
		spaShareError(c, 500, "internal_error")
		return
	}
	grantRows := make([]spaGrant, 0, len(grants))
	for _, g := range grants {
		if g.UserID != deck.OwnerUserID {
			grantRows = append(grantRows, spaGrant{UserID: g.UserID, Username: s.usernameFor(c, g.UserID), Role: g.Role})
		}
	}
	now := time.Now().UTC()
	linkRows := make([]spaShareLink, 0, len(links))
	for _, l := range links {
		linkRows = append(linkRows, spaShareLink{Prefix: shortDigest(l.Token), CreatedAt: l.CreatedAt, ExpiresAt: l.ExpiresAt, HasPassword: l.PasswordHash != nil, Revoked: l.RevokedAt != nil, Expired: l.ExpiresAt != nil && !now.Before(*l.ExpiresAt)})
	}
	visibility := deck.Visibility
	if visibility == "" {
		visibility = store.DeckVisibilityPrivate
	}
	// 待接受的邀请单独一列：属主必须能区分「已授权」与「邀请了还没答应」——后者随时可能
	// 被拒绝，界面上不该显示成已有访问权。
	pending, err := s.shareInvites.ListForDeck(ctx, deck.ID)
	if err != nil {
		s.logger.Error("list SPA deck share invites failed", "deck_id", deck.ID, "error", err)
		spaShareError(c, 500, "internal_error")
		return
	}
	c.JSON(http.StatusOK, gin.H{"deck_id": deck.ID, "deck_name": deck.Name, "visibility": visibility, "grants": grantRows, "pending_invites": pending, "links": linkRows})
}

func (s *Server) spaSharingWrite(c *gin.Context, action string) {
	user, ok := s.spaProfileSessionOnly(c)
	if !ok {
		return
	}
	deckID, ok := deckIDParam(c)
	if !ok {
		spaShareError(c, 404, "not_found")
		return
	}
	deck, ok := s.loadDeckForRole(c, user, deckID, store.RoleOwner)
	if !ok {
		return
	}
	var req spaSharingRequest
	if action == "link_revoke" {
		req.Link = c.Param("digest")
	} else if action == "revoke" {
		id, err := strconv.ParseUint(c.Param("userID"), 10, 64)
		if err != nil {
			spaShareError(c, 400, "invalid_request")
			return
		}
		req.UserID = id
	} else if action != "link_revoke_all" {
		if err := c.ShouldBindJSON(&req); err != nil {
			spaShareError(c, 400, "invalid_request")
			return
		}
	}
	ctx := c.Request.Context()
	bad := func() { spaShareError(c, 400, "invalid_request") }
	switch action {
	case "grant":
		role := strings.TrimSpace(req.Role)
		if !store.ValidRole(role) || role == store.RoleOwner {
			bad()
			return
		}
		var target *store.User
		var err error
		if req.UserID != 0 {
			target, err = s.users.ByID(ctx, req.UserID)
		} else if strings.TrimSpace(req.Username) != "" {
			target, err = s.users.ByUsername(ctx, strings.TrimSpace(req.Username))
		} else {
			id, parseErr := strconv.ParseUint(c.Param("userID"), 10, 64)
			if parseErr != nil || id == 0 {
				bad()
				return
			}
			target, err = s.users.ByID(ctx, id)
		}
		if err != nil {
			spaShareError(c, 404, "not_found")
			return
		}
		if target.ID == deck.OwnerUserID {
			bad()
			return
		}
		existing, err := s.grants.Role(ctx, deck.ID, target.ID)
		if err != nil {
			s.logger.Error("read SPA deck grant failed", "deck_id", deck.ID, "error", err)
			spaShareError(c, 500, "internal_error")
			return
		}
		if existing != role {
			if existing != "" {
				// 已经在里面的人：改角色不需要再征得同意——同意在接受那一刻就给过了。
				if err = s.grants.Grant(ctx, deck.ID, target.ID, role, store.Ptr(user.ID)); err != nil {
					s.logger.Error("grant SPA deck role failed", "deck_id", deck.ID, "error", err)
					spaShareError(c, 500, "internal_error")
					return
				}
				s.audit(ctx, store.AuditEntry{UserID: store.Ptr(user.ID), Action: store.ActionDeckRoleChange, TargetType: "deck", TargetID: store.Ptr(deck.ID), Detail: map[string]any{"username": target.Username, "user_id": target.ID, "role": role, "previous_role": existing}})
				// 通知（B 类，可退订）。放在审计之后、且不返回错误：通知只是副作用。
				s.notifyDeckGrantChange(c, deck, user, target, existing, role)
				break
			}
			// 新人：**发邀请，不直接授权**（同意制）。接受时才写 deck_grants，
			// 所以「别人把卡组硬塞给我」在这条路径上不可能发生。
			allowed, policyErr := s.sharePolicy.Allows(ctx, target.ID, user.ID)
			if policyErr != nil {
				s.logger.Error("check recipient share policy failed", "deck_id", deck.ID, "user_id", target.ID, "error", policyErr)
				spaShareError(c, 500, "internal_error")
				return
			}
			if !allowed {
				// 对方设了「不接受分享」或不在白名单里：如实拒绝，并让属主看到原因。
				spaShareError(c, 409, "recipient_refuses_shares")
				return
			}
			now := time.Now().UTC()
			if err := s.shareInvites.Invite(ctx, store.DeckShareInvite{
				DeckID: deck.ID, UserID: target.ID, Role: role, InvitedBy: user.ID,
				CreatedAt: now, ExpiresAt: now.Add(ShareInviteTTL),
			}); err != nil {
				s.logger.Error("create deck share invite failed", "deck_id", deck.ID, "error", err)
				spaShareError(c, 500, "internal_error")
				return
			}
			s.audit(ctx, store.AuditEntry{UserID: store.Ptr(user.ID), Action: store.ActionDeckShareInvite, TargetType: "deck", TargetID: store.Ptr(deck.ID), Detail: map[string]any{"username": target.Username, "user_id": target.ID, "role": role}})
			s.notifyDeckGrantChange(c, deck, user, target, "", role)
		}
	case "revoke":
		id := req.UserID
		if id == 0 || id == deck.OwnerUserID {
			bad()
			return
		}
		existing, err := s.grants.Role(ctx, deck.ID, id)
		if err != nil {
			s.logger.Error("read SPA deck grant failed", "deck_id", deck.ID, "error", err)
			spaShareError(c, 500, "internal_error")
			return
		}
		if err = s.grants.Revoke(ctx, deck.ID, id); err != nil {
			s.logger.Error("revoke SPA deck grant failed", "deck_id", deck.ID, "error", err)
			spaShareError(c, 500, "internal_error")
			return
		}
		if existing != "" {
			s.audit(ctx, store.AuditEntry{UserID: store.Ptr(user.ID), Action: store.ActionDeckRevoke, TargetType: "deck", TargetID: store.Ptr(deck.ID), Detail: map[string]any{"user_id": id, "previous_role": existing}})
			s.notifyDeckRevoke(c, deck, user, id, existing)
			break
		}
		// 没有生效的授权，但可能挂着一条还没被接受的邀请：这时「撤销」的语义是取消邀请。
		if err := s.shareInvites.Delete(ctx, deck.ID, id); err != nil {
			s.logger.Error("cancel deck share invite failed", "deck_id", deck.ID, "user_id", id, "error", err)
			spaShareError(c, 500, "internal_error")
			return
		}
	case "visibility":
		v := strings.TrimSpace(req.Visibility)
		if err := s.decks.SetVisibility(ctx, user.ID, deck.ID, v); err != nil {
			if errors.Is(err, store.ErrInvalidVisibility) {
				bad()
			} else {
				s.logger.Error("set SPA deck visibility failed", "deck_id", deck.ID, "error", err)
				spaShareError(c, 500, "internal_error")
			}
			return
		}
		if v != deck.Visibility {
			s.audit(ctx, store.AuditEntry{UserID: store.Ptr(user.ID), Action: store.ActionDeckVisibility, TargetType: "deck", TargetID: store.Ptr(deck.ID), Detail: map[string]any{"previous": deck.Visibility, "visibility": v}})
		}
	case "link_create":
		pw := strings.TrimSpace(req.Password)
		var hash *string
		if pw != "" {
			if len(pw) > auth.MaxPasswordLength {
				bad()
				return
			}
			h, err := shareLinkPasswordHasher.Hash(pw)
			if err != nil {
				s.logger.Error("hash SPA share link password failed", "deck_id", deck.ID, "error", err)
				spaShareError(c, 500, "internal_error")
				return
			}
			hash = &h
		}
		var expiry *time.Time
		expiryDetail := ""
		if raw := strings.TrimSpace(req.ExpiresAt); raw != "" {
			day, err := time.Parse("2006-01-02", raw)
			if err != nil {
				bad()
				return
			}
			end := day.AddDate(0, 0, 1)
			expiry = &end
			expiryDetail = end.UTC().Format(time.RFC3339)
		}
		plain, _, err := s.shareLinks.Create(ctx, store.ShareLinkInput{DeckID: deck.ID, PasswordHash: hash, ExpiresAt: expiry, CreatedBy: user.ID})
		if err != nil {
			s.logger.Error("create SPA share link failed", "deck_id", deck.ID, "error", err)
			spaShareError(c, 500, "internal_error")
			return
		}
		s.audit(ctx, store.AuditEntry{UserID: store.Ptr(user.ID), Action: store.ActionShareLinkCreate, TargetType: "deck", TargetID: store.Ptr(deck.ID), Detail: map[string]any{"has_password": hash != nil, "expires_at": expiryDetail}})
		c.JSON(http.StatusCreated, gin.H{"link": "/s/" + plain})
		return
	case "link_revoke":
		digest := strings.TrimSpace(req.Link)
		if len(digest) != 64 {
			bad()
			return
		}
		if _, err := strconv.ParseUint(digest[:16], 16, 64); err != nil {
			bad()
			return
		}
		if err := s.shareLinks.Revoke(ctx, deck.ID, digest); err != nil {
			s.logger.Error("revoke SPA share link failed", "deck_id", deck.ID, "error", err)
			spaShareError(c, 500, "internal_error")
			return
		}
		s.audit(ctx, store.AuditEntry{UserID: store.Ptr(user.ID), Action: store.ActionShareLinkRevoke, TargetType: "deck", TargetID: store.Ptr(deck.ID), Detail: map[string]any{"link": shortDigest(digest)}})
	case "link_revoke_all":
		n, err := s.shareLinks.RevokeAll(ctx, deck.ID)
		if err != nil {
			s.logger.Error("revoke all SPA share links failed", "deck_id", deck.ID, "error", err)
			spaShareError(c, 500, "internal_error")
			return
		}
		s.audit(ctx, store.AuditEntry{UserID: store.Ptr(user.ID), Action: store.ActionShareLinkRevokeAll, TargetType: "deck", TargetID: store.Ptr(deck.ID), Detail: map[string]any{"revoked": n}})
	default:
		bad()
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
func spaShareError(c *gin.Context, status int, code string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": api.ErrorMessage(c.Request.Context(), code)}})
}
func (s *Server) spaSharingGrant(c *gin.Context)         { s.spaSharingWrite(c, "grant") }
func (s *Server) spaSharingRevoke(c *gin.Context)        { s.spaSharingWrite(c, "revoke") }
func (s *Server) spaSharingVisibility(c *gin.Context)    { s.spaSharingWrite(c, "visibility") }
func (s *Server) spaSharingLinkCreate(c *gin.Context)    { s.spaSharingWrite(c, "link_create") }
func (s *Server) spaSharingLinkRevoke(c *gin.Context)    { s.spaSharingWrite(c, "link_revoke") }
func (s *Server) spaSharingLinkRevokeAll(c *gin.Context) { s.spaSharingWrite(c, "link_revoke_all") }
