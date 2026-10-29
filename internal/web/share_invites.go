package web

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the server side of the "share consent model":
//
//   - what the owner sends is an **invite**, written to deck_share_invites, not to deck_grants;
//   - the invitee accepts or rejects it from their own list;
//   - only acceptance writes deck_grants — and only then does it join the visible set.
//
// So "someone forcing a deck on me" is structurally impossible: no acceptance, no grant. Users can also, with the
// receiving policy (anyone / whitelist / nobody), block it **before** the invite goes out.

// ShareInviteTTL is the invite lifetime.
//
// 30 days: shorter than the "forgot about it" timescale, longer than "did not check mail over the weekend". Expired rows are
// reclaimed hourly by internal/retention, not deleted on the read path.
const ShareInviteTTL = 30 * 24 * time.Hour

// shareAllowRow is one row of the allow list. It carries the username because the UI has to show "who", and a bare id is not readable;
// user_id is the public id, numeric primary keys are not exposed.
type shareAllowRow struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
}

// shareInvitesResponse is the list of pending invites.
type shareInvitesResponse struct {
	Invites []store.DeckShareInviteView `json:"invites"`
	// Policy comes back together with AllowList: in the UI, "pending" and "who may share with me" are two sides of the same thing.
	Policy    store.ShareAcceptPolicy `json:"policy"`
	AllowList []shareAllowRow         `json:"allow_list"`
}

// sharePolicyRequest is the write request for the receiving policy.
//
// The allow list takes **usernames** (same criterion as the sharing grant endpoints): what the UI can show is usernames, and making the frontend
// translate names into ids would mean exposing another user-search endpoint, and "who can invite me" should not have that precondition.
// Allow / Revoke also accept the public id form (for scripts and tests); both paths resolve to numeric primary keys.
type sharePolicyRequest struct {
	Policy string `json:"policy"`
	// Allow and Revoke are the public user ids to "add to the allow list" and "remove from the allow list" respectively.
	Allow  []string `json:"allow,omitempty"`
	Revoke []string `json:"revoke,omitempty"`
	// AllowUsernames and RevokeUsernames are the username form of the above.
	AllowUsernames  []string `json:"allow_usernames,omitempty"`
	RevokeUsernames []string `json:"revoke_usernames,omitempty"`
}

// shareAllowRows turns a list of allow-list primary keys into rows carrying the username and public id; rows whose user no longer exists are skipped
// without falling back to the numeric primary key.
func (s *Server) shareAllowRows(c *gin.Context, userID uint64) ([]shareAllowRow, error) {
	ids, err := s.sharePolicy.AllowList(c.Request.Context(), userID)
	if err != nil {
		return nil, err
	}
	ctx := c.Request.Context()
	rows := make([]shareAllowRow, 0, len(ids))
	for _, id := range ids {
		u, err := s.users.ByID(ctx, id)
		if err != nil || u == nil {
			continue
		}
		rows = append(rows, shareAllowRow{UserID: u.PublicID, Username: u.Username})
	}
	return rows, nil
}

// resolveShareUserIDs turns a batch of user public ids into primary keys; any unknown one writes 400 and returns false.
func (s *Server) resolveShareUserIDs(c *gin.Context, ids []string) ([]uint64, bool) {
	ctx := c.Request.Context()
	out := make([]uint64, 0, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		u, err := s.users.ByPublicID(ctx, id)
		if err != nil {
			shareError(c, http.StatusBadRequest, "user_not_found")
			return nil, false
		}
		out = append(out, u.ID)
	}
	return out, true
}

// resolveShareTargets turns a batch of usernames into ids; if any cannot be found it returns false (the 400 response is already written).
func (s *Server) resolveShareTargets(c *gin.Context, names []string) ([]uint64, bool) {
	ctx := c.Request.Context()
	out := make([]uint64, 0, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		u, err := s.users.ByUsername(ctx, name)
		if err != nil {
			shareError(c, http.StatusBadRequest, "user_not_found")
			return nil, false
		}
		out = append(out, u.ID)
	}
	return out, true
}

// shareInviteList returns the current user's pending invites, receiving policy and allow list (GET /api/v1/sharing/invites).
func (s *Server) shareInviteList(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		shareError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx := c.Request.Context()
	invites, err := s.shareInvites.ListForUser(ctx, u.ID)
	if err != nil {
		s.logger.Error("list share invites failed", "user_id", u.ID, "error", err)
		shareError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	policy, err := s.sharePolicy.Policy(ctx, u.ID)
	if err != nil {
		s.logger.Error("load share policy failed", "user_id", u.ID, "error", err)
		shareError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	allow, err := s.shareAllowRows(c, u.ID)
	if err != nil {
		s.logger.Error("load share allow list failed", "user_id", u.ID, "error", err)
		shareError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	c.JSON(http.StatusOK, shareInvitesResponse{Invites: invites, Policy: policy, AllowList: allow})
}

// shareInviteDeckID parses the deck public id from the path and swaps it for the numeric primary key; unknown or empty is always 404 invite_not_found.
func (s *Server) shareInviteDeckID(c *gin.Context) (uint64, bool) {
	raw := strings.TrimSpace(c.Param("deckID"))
	if raw == "" {
		shareError(c, http.StatusNotFound, "invite_not_found")
		return 0, false
	}
	deck, err := s.decks.ByPublicID(c.Request.Context(), raw)
	if err != nil {
		shareError(c, http.StatusNotFound, "invite_not_found")
		return 0, false
	}
	return deck.ID, true
}

// shareInviteAccept accepts one invite: write the grant, delete the invite, write the audit (POST …/accept).
func (s *Server) shareInviteAccept(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		shareError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	deckID, ok := s.shareInviteDeckID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	inv, err := s.shareInvites.ByDeckAndUser(ctx, deckID, u.ID)
	if err != nil {
		// An expired row may not have been reclaimed yet: the list drops it on the read path, and this adds one more check.
		shareError(c, http.StatusNotFound, "invite_not_found")
		return
	}
	if !inv.ExpiresAt.After(time.Now().UTC()) {
		// Expired means void, and it is cleaned up along the way so it does not show up in the list again.
		_ = s.shareInvites.Delete(ctx, deckID, u.ID)
		shareError(c, http.StatusNotFound, "invite_expired")
		return
	}
	if _, err := s.decks.ByID(ctx, deckID); err != nil {
		// The deck is already deleted: the invite is void along with it.
		_ = s.shareInvites.Delete(ctx, deckID, u.ID)
		shareError(c, http.StatusNotFound, "invite_not_found")
		return
	}
	if err := s.grants.Grant(ctx, deckID, u.ID, inv.Role, store.Ptr(inv.InvitedBy)); err != nil {
		s.logger.Error("accept share invite failed", "deck_id", deckID, "user_id", u.ID, "error", err)
		shareError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	if err := s.shareInvites.Delete(ctx, deckID, u.ID); err != nil {
		s.logger.Error("delete accepted share invite failed", "deck_id", deckID, "user_id", u.ID, "error", err)
	}
	s.auditor.Record(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID), Action: store.ActionDeckShareAccept,
		TargetType: "deck", TargetID: store.Ptr(deckID),
		Detail: map[string]any{"role": inv.Role, "invited_by": s.userPublicID(ctx, inv.InvitedBy)},
	})
	c.JSON(http.StatusOK, gin.H{"accepted": true})
}

// shareInviteReject rejects one invite: delete the invite, write the audit, and no grant ever appears (POST …/reject).
func (s *Server) shareInviteReject(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		shareError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	deckID, ok := s.shareInviteDeckID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	inv, err := s.shareInvites.ByDeckAndUser(ctx, deckID, u.ID)
	if err != nil {
		shareError(c, http.StatusNotFound, "invite_not_found")
		return
	}
	if err := s.shareInvites.Delete(ctx, deckID, u.ID); err != nil {
		s.logger.Error("reject share invite failed", "deck_id", deckID, "user_id", u.ID, "error", err)
		shareError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	s.auditor.Record(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID), Action: store.ActionDeckShareReject,
		TargetType: "deck", TargetID: store.Ptr(deckID),
		Detail: map[string]any{"invited_by": s.userPublicID(ctx, inv.InvitedBy)},
	})
	c.JSON(http.StatusOK, gin.H{"rejected": true})
}

// sharePolicyGet returns the receiving policy and allow list (GET /api/v1/settings/share-policy).
func (s *Server) sharePolicyGet(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		shareError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx := c.Request.Context()
	policy, err := s.sharePolicy.Policy(ctx, u.ID)
	if err != nil {
		s.logger.Error("load share policy failed", "user_id", u.ID, "error", err)
		shareError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	allow, err := s.shareAllowRows(c, u.ID)
	if err != nil {
		s.logger.Error("load share allow list failed", "user_id", u.ID, "error", err)
		shareError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	c.JSON(http.StatusOK, gin.H{"policy": policy, "allow_list": allow})
}

// sharePolicySave saves the receiving policy and allow-list additions/removals (PUT /api/v1/settings/share-policy).
//
// Policy and allow list can be submitted together: in the UI the three switches and the list are one thing. An empty policy means the policy is unchanged.
func (s *Server) sharePolicySave(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		shareError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req sharePolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		shareError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	ctx := c.Request.Context()
	policyRaw := strings.TrimSpace(req.Policy)
	if policyRaw != "" {
		switch store.ShareAcceptPolicy(policyRaw) {
		case store.ShareAcceptAnyone, store.ShareAcceptWhitelist, store.ShareAcceptNobody:
		default:
			shareError(c, http.StatusBadRequest, "invalid_policy")
			return
		}
		if err := s.sharePolicy.SetPolicy(ctx, u.ID, store.ShareAcceptPolicy(policyRaw)); err != nil {
			s.logger.Error("save share policy failed", "user_id", u.ID, "error", err)
			shareError(c, http.StatusInternalServerError, "internal_error")
			return
		}
	}
	// The username and public id forms are handled together: the UI submits usernames (it only knows names), tests and scripts submit public ids.
	byName, ok := s.resolveShareTargets(c, req.AllowUsernames)
	if !ok {
		return
	}
	revokeByName, ok := s.resolveShareTargets(c, req.RevokeUsernames)
	if !ok {
		return
	}
	byPublicID, ok := s.resolveShareUserIDs(c, req.Allow)
	if !ok {
		return
	}
	revokeByPublicID, ok := s.resolveShareUserIDs(c, req.Revoke)
	if !ok {
		return
	}
	allowIDs := append(append([]uint64{}, byPublicID...), byName...)
	revokeIDs := append(append([]uint64{}, revokeByPublicID...), revokeByName...)
	for _, from := range allowIDs {
		if from == 0 || from == u.ID {
			continue
		}
		if err := s.sharePolicy.Allow(ctx, u.ID, from); err != nil {
			s.logger.Error("add share allow failed", "user_id", u.ID, "error", err)
			shareError(c, http.StatusInternalServerError, "internal_error")
			return
		}
	}
	for _, from := range revokeIDs {
		if err := s.sharePolicy.RevokeAllow(ctx, u.ID, from); err != nil {
			s.logger.Error("remove share allow failed", "user_id", u.ID, "error", err)
			shareError(c, http.StatusInternalServerError, "internal_error")
			return
		}
	}
	s.auditor.Record(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID), Action: store.ActionSharePolicyUpdate,
		TargetType: "user", TargetID: store.Ptr(u.ID),
		Detail: map[string]any{"policy": policyRaw, "allow": len(allowIDs), "revoke": len(revokeIDs)},
	})
	// Read back after writing: the allow list is a union operation, so a list inferred locally need not equal the truth in the database.
	policy, _ := s.sharePolicy.Policy(ctx, u.ID)
	allow, _ := s.shareAllowRows(c, u.ID)
	c.JSON(http.StatusOK, gin.H{"policy": policy, "allow_list": allow})
}

// registerShareInviteRoutes mounts the share consent endpoints (M5-*). Skips when sessions are not wired.
func (s *Server) registerShareInviteRoutes(router *gin.Engine) {
	if s.sessions == nil || s.shareInvites == nil || s.sharePolicy == nil {
		return
	}
	router.GET("/api/v1/sharing/invites", s.shareInviteList)
	router.POST("/api/v1/sharing/invites/:deckID/accept", s.sessions.CSRFMiddleware(), s.shareInviteAccept)
	router.POST("/api/v1/sharing/invites/:deckID/reject", s.sessions.CSRFMiddleware(), s.shareInviteReject)
	router.GET("/api/v1/settings/share-policy", s.sharePolicyGet)
	router.PUT("/api/v1/settings/share-policy", s.sessions.CSRFMiddleware(), s.sharePolicySave)
}
