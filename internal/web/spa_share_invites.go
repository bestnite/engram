package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是「分享同意制」的服务端：
//
//   - 属主发出的是**邀请**，写 deck_share_invites，不写 deck_grants；
//   - 被邀请者在自己的列表里接受或拒绝；
//   - 接受才写 deck_grants——这时才进可见集合。
//
// 因此「别人把卡组硬塞给我」在结构上不可能：没有接受就没有授权。另外用户可以用
// 接收策略（anyone / whitelist / nobody）在邀请发出**之前**就拦住。

// ShareInviteTTL 是邀请的有效期。
//
// 30 天：短于「忘了这回事」的时间尺度，长于「周末没看邮件」的尺度。到期行由
// internal/retention 每小时回收，不在读路径上删。
const ShareInviteTTL = 30 * 24 * time.Hour

// spaShareAllowRow 是白名单里的一行。带用户名是因为界面要显示"谁"，而裸 id 不可读。
type spaShareAllowRow struct {
	UserID   uint64 `json:"user_id"`
	Username string `json:"username"`
}

// spaShareInvitesResponse 是待接受邀请的列表。
type spaShareInvitesResponse struct {
	Invites []store.DeckShareInviteView `json:"invites"`
	// Policy 与 AllowList 一起返回：界面上「待接受」与「谁能分享给我」是同一件事的两面。
	Policy    store.ShareAcceptPolicy `json:"policy"`
	AllowList []spaShareAllowRow      `json:"allow_list"`
}

// spaSharePolicyRequest 是接收策略的写入请求。
//
// 白名单按**用户名**收（与共享授权端点同一口径）：界面能显示的是用户名，要求前端自己
// 把名字翻成 id 就得再暴露一个用户搜索端点，而"谁能邀请我"不该有这个前置条件。
type spaSharePolicyRequest struct {
	Policy string `json:"policy"`
	// Allow 与 Revoke 分别是"加进白名单""移出白名单"的用户 id。
	Allow  []uint64 `json:"allow,omitempty"`
	Revoke []uint64 `json:"revoke,omitempty"`
	// AllowUsernames 与 RevokeUsernames 同上的用户名写法。
	AllowUsernames  []string `json:"allow_usernames,omitempty"`
	RevokeUsernames []string `json:"revoke_usernames,omitempty"`
}

// shareAllowRows 把白名单 id 列表翻成带用户名的行（id 查不到就留空名，行仍保留）。
func (s *Server) shareAllowRows(c *gin.Context, userID uint64) ([]spaShareAllowRow, error) {
	ids, err := s.sharePolicy.AllowList(c.Request.Context(), userID)
	if err != nil {
		return nil, err
	}
	rows := make([]spaShareAllowRow, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, spaShareAllowRow{UserID: id, Username: s.usernameFor(c, id)})
	}
	return rows, nil
}

// resolveShareTargets 把一批用户名翻成 id；任何一个查不到都返回 false（已写好 400 响应）。
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
			spaShareError(c, http.StatusBadRequest, "user_not_found")
			return nil, false
		}
		out = append(out, u.ID)
	}
	return out, true
}

// spaShareInvites 返回当前用户待接受的邀请、接收策略与白名单（GET /api/v1/sharing/invites）。
func (s *Server) spaShareInvites(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		spaShareError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx := c.Request.Context()
	invites, err := s.shareInvites.ListForUser(ctx, u.ID)
	if err != nil {
		s.logger.Error("list share invites failed", "user_id", u.ID, "error", err)
		spaShareError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	policy, err := s.sharePolicy.Policy(ctx, u.ID)
	if err != nil {
		s.logger.Error("load share policy failed", "user_id", u.ID, "error", err)
		spaShareError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	allow, err := s.shareAllowRows(c, u.ID)
	if err != nil {
		s.logger.Error("load share allow list failed", "user_id", u.ID, "error", err)
		spaShareError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	c.JSON(http.StatusOK, spaShareInvitesResponse{Invites: invites, Policy: policy, AllowList: allow})
}

// shareInviteDeckID 解析路径里的卡组 id；不合法时已写好响应。
func shareInviteDeckID(c *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(strings.TrimSpace(c.Param("deckID")), 10, 64)
	if err != nil || id == 0 {
		spaShareError(c, http.StatusBadRequest, "invalid_request")
		return 0, false
	}
	return id, true
}

// spaShareInviteAccept 接受一条邀请：写授权、删邀请、写审计（POST …/accept）。
func (s *Server) spaShareInviteAccept(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		spaShareError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	deckID, ok := shareInviteDeckID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	inv, err := s.shareInvites.ByDeckAndUser(ctx, deckID, u.ID)
	if err != nil {
		// 过期行可能还没被回收：由 store 在读路径上判掉的是列表，这里补一道。
		spaShareError(c, http.StatusNotFound, "invite_not_found")
		return
	}
	if !inv.ExpiresAt.After(time.Now().UTC()) {
		// 过期即作废，并且顺手清掉，免得列表里再出现。
		_ = s.shareInvites.Delete(ctx, deckID, u.ID)
		spaShareError(c, http.StatusNotFound, "invite_expired")
		return
	}
	if _, err := s.decks.ByID(ctx, deckID); err != nil {
		// 卡组已被删：邀请随之作废。
		_ = s.shareInvites.Delete(ctx, deckID, u.ID)
		spaShareError(c, http.StatusNotFound, "invite_not_found")
		return
	}
	if err := s.grants.Grant(ctx, deckID, u.ID, inv.Role, store.Ptr(inv.InvitedBy)); err != nil {
		s.logger.Error("accept share invite failed", "deck_id", deckID, "user_id", u.ID, "error", err)
		spaShareError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	if err := s.shareInvites.Delete(ctx, deckID, u.ID); err != nil {
		s.logger.Error("delete accepted share invite failed", "deck_id", deckID, "user_id", u.ID, "error", err)
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID), Action: store.ActionDeckShareAccept,
		TargetType: "deck", TargetID: store.Ptr(deckID),
		Detail: map[string]any{"role": inv.Role, "invited_by": inv.InvitedBy},
	})
	c.JSON(http.StatusOK, gin.H{"accepted": true})
}

// spaShareInviteReject 拒绝一条邀请：删邀请、写审计，授权从不出现（POST …/reject）。
func (s *Server) spaShareInviteReject(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		spaShareError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	deckID, ok := shareInviteDeckID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	inv, err := s.shareInvites.ByDeckAndUser(ctx, deckID, u.ID)
	if err != nil {
		spaShareError(c, http.StatusNotFound, "invite_not_found")
		return
	}
	if err := s.shareInvites.Delete(ctx, deckID, u.ID); err != nil {
		s.logger.Error("reject share invite failed", "deck_id", deckID, "user_id", u.ID, "error", err)
		spaShareError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID), Action: store.ActionDeckShareReject,
		TargetType: "deck", TargetID: store.Ptr(deckID),
		Detail: map[string]any{"invited_by": inv.InvitedBy},
	})
	c.JSON(http.StatusOK, gin.H{"rejected": true})
}

// spaSharePolicyGet 返回接收策略与白名单（GET /api/v1/settings/share-policy）。
func (s *Server) spaSharePolicyGet(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		spaShareError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx := c.Request.Context()
	policy, err := s.sharePolicy.Policy(ctx, u.ID)
	if err != nil {
		s.logger.Error("load share policy failed", "user_id", u.ID, "error", err)
		spaShareError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	allow, err := s.shareAllowRows(c, u.ID)
	if err != nil {
		s.logger.Error("load share allow list failed", "user_id", u.ID, "error", err)
		spaShareError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	c.JSON(http.StatusOK, gin.H{"policy": policy, "allow_list": allow})
}

// spaSharePolicySave 保存接收策略与白名单增删（PUT /api/v1/settings/share-policy）。
//
// 策略与白名单可以一起提交：界面上的三个开关与名单是一件事。空 policy 表示不改策略。
func (s *Server) spaSharePolicySave(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		spaShareError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req spaSharePolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		spaShareError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	ctx := c.Request.Context()
	policyRaw := strings.TrimSpace(req.Policy)
	if policyRaw != "" {
		switch store.ShareAcceptPolicy(policyRaw) {
		case store.ShareAcceptAnyone, store.ShareAcceptWhitelist, store.ShareAcceptNobody:
		default:
			spaShareError(c, http.StatusBadRequest, "invalid_policy")
			return
		}
		if err := s.sharePolicy.SetPolicy(ctx, u.ID, store.ShareAcceptPolicy(policyRaw)); err != nil {
			s.logger.Error("save share policy failed", "user_id", u.ID, "error", err)
			spaShareError(c, http.StatusInternalServerError, "internal_error")
			return
		}
	}
	// 用户名与 id 两种写法合并处理：界面提交用户名（它只认识名字），测试与脚本提交 id。
	byName, ok := s.resolveShareTargets(c, req.AllowUsernames)
	if !ok {
		return
	}
	revokeByName, ok := s.resolveShareTargets(c, req.RevokeUsernames)
	if !ok {
		return
	}
	allowIDs := append(append([]uint64{}, req.Allow...), byName...)
	revokeIDs := append(append([]uint64{}, req.Revoke...), revokeByName...)
	for _, from := range allowIDs {
		if from == 0 || from == u.ID {
			continue
		}
		if err := s.sharePolicy.Allow(ctx, u.ID, from); err != nil {
			s.logger.Error("add share allow failed", "user_id", u.ID, "error", err)
			spaShareError(c, http.StatusInternalServerError, "internal_error")
			return
		}
	}
	for _, from := range revokeIDs {
		if err := s.sharePolicy.RevokeAllow(ctx, u.ID, from); err != nil {
			s.logger.Error("remove share allow failed", "user_id", u.ID, "error", err)
			spaShareError(c, http.StatusInternalServerError, "internal_error")
			return
		}
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID), Action: store.ActionSharePolicyUpdate,
		TargetType: "user", TargetID: store.Ptr(u.ID),
		Detail: map[string]any{"policy": policyRaw, "allow": len(allowIDs), "revoke": len(revokeIDs)},
	})
	// 写后回读：白名单是并集操作，本地推断出的列表未必等于库里的真值。
	policy, _ := s.sharePolicy.Policy(ctx, u.ID)
	allow, _ := s.shareAllowRows(c, u.ID)
	c.JSON(http.StatusOK, gin.H{"policy": policy, "allow_list": allow})
}

// registerShareInviteRoutes 挂载分享同意制的端点（M5-*）。会话未装配时跳过。
func (s *Server) registerShareInviteRoutes(router *gin.Engine) {
	if s.sessions == nil || s.shareInvites == nil || s.sharePolicy == nil {
		return
	}
	router.GET("/api/v1/sharing/invites", s.spaShareInvites)
	router.POST("/api/v1/sharing/invites/:deckID/accept", s.sessions.CSRFMiddleware(), s.spaShareInviteAccept)
	router.POST("/api/v1/sharing/invites/:deckID/reject", s.sessions.CSRFMiddleware(), s.spaShareInviteReject)
	router.GET("/api/v1/settings/share-policy", s.spaSharePolicyGet)
	router.PUT("/api/v1/settings/share-policy", s.sessions.CSRFMiddleware(), s.spaSharePolicySave)
}
