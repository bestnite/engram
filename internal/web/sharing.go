package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"example.com/flashcard/internal/auth"
	"example.com/flashcard/internal/i18n"
	"example.com/flashcard/internal/store"
	"example.com/flashcard/internal/web/views"
)

// registerSharingRoutes 挂载共享管理页（M5-2）。
//
// 共享管理页只对 owner 开放（判定走 auth.DeckAccess）。写操作一律过 CSRF 中间件。
// 依赖未装配时跳过，保证 M0 阶段的测试仍能构造 Server。
func (s *Server) registerSharingRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.grants == nil || s.users == nil {
		return
	}
	router.GET("/decks/:id/sharing", s.sharingPage)
	router.POST("/decks/:id/sharing/grant", s.sessions.CSRFMiddleware(), s.sharingGrant)
	router.POST("/decks/:id/sharing/revoke", s.sessions.CSRFMiddleware(), s.sharingRevoke)
}

// roleOptions 生成可授予的角色下拉：owner 不可授予（归属只能由 owner_user_id 决定）。
func (s *Server) roleOptions(loc *i18n.Localizer, current string) []views.RoleOption {
	roles := []string{store.RoleEditor, store.RoleReader}
	opts := make([]views.RoleOption, 0, len(roles))
	for _, r := range roles {
		opts = append(opts, views.RoleOption{
			Value:    r,
			Label:    loc.T("sharing.role." + r),
			Selected: r == current,
		})
	}
	return opts
}

// usernameFor 解析用户 id 对应的显示名；查不到时退回 #id，保证列表不因单个坏行而失败。
func (s *Server) usernameFor(c *gin.Context, userID uint64) string {
	u, err := s.users.ByID(c.Request.Context(), userID)
	if err != nil || u == nil {
		return "#" + strconv.FormatUint(userID, 10)
	}
	return u.Username
}

// sharingPage 渲染共享管理页；只有 owner 能打开（非 owner 由 loadDeckForRole 写 403 + 审计）。
func (s *Server) sharingPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	deckID, ok := deckIDParam(c)
	if !ok {
		return
	}
	deck, ok := s.loadDeckForRole(c, user, deckID, store.RoleOwner)
	if !ok {
		return
	}
	s.renderSharing(c, loc, deck, http.StatusOK, "")
}

// renderSharing 装配共享管理页数据并写出；status 用于把校验失败渲染成 4xx。
func (s *Server) renderSharing(c *gin.Context, loc *i18n.Localizer, deck *store.Deck, status int, errMsg string) {
	ctx := c.Request.Context()
	grants, err := s.grants.ListByDeck(ctx, deck.ID)
	if err != nil {
		s.logger.Error("list deck grants failed", "deck_id", deck.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	// 首行固定是 owner：owner_user_id 是归属的唯一真相，owner 行不可改、不可撤销。
	rows := make([]views.SharingRow, 0, len(grants)+1)
	rows = append(rows, views.SharingRow{
		UserValue: strconv.FormatUint(deck.OwnerUserID, 10),
		Username:  s.usernameFor(c, deck.OwnerUserID),
		RoleValue: store.RoleOwner,
		RoleLabel: loc.T("sharing.role.owner"),
		Editable:  false,
	})
	for i := range grants {
		g := grants[i]
		if g.UserID == deck.OwnerUserID {
			continue
		}
		rows = append(rows, views.SharingRow{
			UserValue:    strconv.FormatUint(g.UserID, 10),
			Username:     s.usernameFor(c, g.UserID),
			RoleValue:    g.Role,
			RoleLabel:    loc.T("sharing.role." + g.Role),
			Editable:     true,
			RoleOptions:  s.roleOptions(loc, g.Role),
			RoleAction:   fmt.Sprintf("/decks/%d/sharing/grant", deck.ID),
			RevokeAction: fmt.Sprintf("/decks/%d/sharing/revoke", deck.ID),
			ChangeLabel:  loc.T("sharing.change_submit"),
			RevokeLabel:  loc.T("sharing.revoke_submit"),
		})
	}

	data := views.SharingData{
		Layout:              s.pageLayout(c, loc, "sharing.title"),
		Heading:             loc.T("sharing.heading"),
		DeckName:            deck.Name,
		ColUser:             loc.T("sharing.col_user"),
		ColRole:             loc.T("sharing.col_role"),
		ColActions:          loc.T("sharing.col_actions"),
		OwnerNote:           loc.T("sharing.owner_note"),
		Rows:                rows,
		EmptyText:           loc.T("sharing.empty"),
		GrantHeading:        loc.T("sharing.grant.heading"),
		GrantAction:         fmt.Sprintf("/decks/%d/sharing/grant", deck.ID),
		UsernameLabel:       loc.T("sharing.grant.username_label"),
		UsernamePlaceholder: loc.T("sharing.grant.username_placeholder"),
		RoleLabel:           loc.T("sharing.grant.role_label"),
		RoleOptions:         s.roleOptions(loc, store.RoleEditor),
		GrantSubmit:         loc.T("sharing.grant.submit"),
		ErrorMessage:        errMsg,
	}
	if sess, ok := auth.CurrentSession(c); ok {
		data.CSRF = sess.CSRFToken
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(status)
	if err := views.SharingPage(data).Render(c.Request.Context(), c.Writer); err != nil {
		s.logger.Error("render template failed", "error", err, "path", c.Request.URL.Path)
	}
}

// resolveGrantTarget 解析授权目标用户：优先 user_id（行内「改角色」表单），否则用 username（新增表单）。
// 解析失败时渲染 4xx 并返回 false。
func (s *Server) resolveGrantTarget(c *gin.Context, loc *i18n.Localizer, deck *store.Deck) (*store.User, bool) {
	ctx := c.Request.Context()
	if raw := strings.TrimSpace(c.PostForm("user_id")); raw != "" {
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || id == 0 {
			s.renderSharing(c, loc, deck, http.StatusBadRequest, loc.T("sharing.error.user_not_found"))
			return nil, false
		}
		u, err := s.users.ByID(ctx, id)
		if err != nil {
			s.renderSharing(c, loc, deck, http.StatusNotFound, loc.T("sharing.error.user_not_found"))
			return nil, false
		}
		return u, true
	}
	username := strings.TrimSpace(c.PostForm("username"))
	if username == "" {
		s.renderSharing(c, loc, deck, http.StatusBadRequest, loc.T("sharing.error.username_required"))
		return nil, false
	}
	u, err := s.users.ByUsername(ctx, username)
	if err != nil {
		s.logger.Info("grant target lookup failed", "deck_id", deck.ID, "username", username, "error", err)
		s.renderSharing(c, loc, deck, http.StatusNotFound, loc.T("sharing.error.user_not_found"))
		return nil, false
	}
	return u, true
}

// sharingGrant 授予或修改角色（仅 owner）。
// 目标此前无授权 -> deck.grant；已有授权且角色变化 -> deck.role_change；角色未变则不改库不写审计。
func (s *Server) sharingGrant(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	deckID, ok := deckIDParam(c)
	if !ok {
		return
	}
	deck, ok := s.loadDeckForRole(c, user, deckID, store.RoleOwner)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	role := strings.TrimSpace(c.PostForm("role"))
	if !store.ValidRole(role) || role == store.RoleOwner {
		s.renderSharing(c, loc, deck, http.StatusBadRequest, loc.T("sharing.error.invalid_role"))
		return
	}
	target, ok := s.resolveGrantTarget(c, loc, deck)
	if !ok {
		return
	}
	if target.ID == deck.OwnerUserID {
		s.renderSharing(c, loc, deck, http.StatusBadRequest, loc.T("sharing.error.owner_immutable"))
		return
	}

	existing, err := s.grants.Role(ctx, deck.ID, target.ID)
	if err != nil {
		s.logger.Error("read deck grant failed", "deck_id", deck.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if existing == role {
		// 幂等：请求的角色与现状一致，不改库也不写审计。
		c.Redirect(http.StatusSeeOther, fmt.Sprintf("/decks/%d/sharing", deck.ID))
		return
	}
	if err := s.grants.Grant(ctx, deck.ID, target.ID, role, store.Ptr(user.ID)); err != nil {
		s.logger.Error("grant deck role failed", "deck_id", deck.ID, "error", err)
		s.renderSharing(c, loc, deck, http.StatusInternalServerError, loc.T("sharing.error.grant_failed"))
		return
	}
	action := store.ActionDeckGrant
	if existing != "" {
		action = store.ActionDeckRoleChange
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     action,
		TargetType: "deck",
		TargetID:   store.Ptr(deck.ID),
		Detail:     map[string]any{"username": target.Username, "user_id": target.ID, "role": role, "previous_role": existing},
	})
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/decks/%d/sharing", deck.ID))
}

// sharingRevoke 撤销授权（仅 owner）；删除授权行后对方下一个请求即被拒（DESIGN.md §5）。
func (s *Server) sharingRevoke(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	deckID, ok := deckIDParam(c)
	if !ok {
		return
	}
	deck, ok := s.loadDeckForRole(c, user, deckID, store.RoleOwner)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	raw := strings.TrimSpace(c.PostForm("user_id"))
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		s.renderSharing(c, loc, deck, http.StatusBadRequest, loc.T("sharing.error.user_not_found"))
		return
	}
	if id == deck.OwnerUserID {
		s.renderSharing(c, loc, deck, http.StatusBadRequest, loc.T("sharing.error.owner_immutable"))
		return
	}
	existing, err := s.grants.Role(ctx, deck.ID, id)
	if err != nil {
		s.logger.Error("read deck grant failed", "deck_id", deck.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if err := s.grants.Revoke(ctx, deck.ID, id); err != nil {
		s.logger.Error("revoke deck grant failed", "deck_id", deck.ID, "error", err)
		s.renderSharing(c, loc, deck, http.StatusInternalServerError, loc.T("sharing.error.grant_failed"))
		return
	}
	if existing != "" {
		s.audit(ctx, store.AuditEntry{
			UserID:     store.Ptr(user.ID),
			Action:     store.ActionDeckRevoke,
			TargetType: "deck",
			TargetID:   store.Ptr(deck.ID),
			Detail:     map[string]any{"user_id": id, "previous_role": existing},
		})
	}
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/decks/%d/sharing", deck.ID))
}
