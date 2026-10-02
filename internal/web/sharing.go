package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"example.com/engram/internal/auth"
	"example.com/engram/internal/i18n"
	"example.com/engram/internal/store"
	"example.com/engram/internal/web/views"
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
	// M5-3 分享链接与 M5-5 可见性：同样是 owner 专属的写操作，一律过 CSRF 中间件。
	router.POST("/decks/:id/sharing/links", s.sessions.CSRFMiddleware(), s.sharingLinkCreate)
	router.POST("/decks/:id/sharing/links/revoke", s.sessions.CSRFMiddleware(), s.sharingLinkRevoke)
	router.POST("/decks/:id/sharing/links/revoke-all", s.sessions.CSRFMiddleware(), s.sharingLinkRevokeAll)
	router.POST("/decks/:id/sharing/visibility", s.sessions.CSRFMiddleware(), s.sharingVisibility)
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
	s.renderSharingPage(c, loc, deck, status, errMsg, "")
}

// renderSharingPage 是 renderSharing 的完整版：newLinkURL 非空时把刚创建的分享链接
// 明文显示一次（明文只存这一次机会，DESIGN.md §11）。
func (s *Server) renderSharingPage(c *gin.Context, loc *i18n.Localizer, deck *store.Deck, status int, errMsg, newLinkURL string) {
	ctx := c.Request.Context()
	grants, err := s.grants.ListByDeck(ctx, deck.ID)
	if err != nil {
		s.logger.Error("list deck grants failed", "deck_id", deck.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	links, err := s.shareLinks.ListByDeck(ctx, deck.ID)
	if err != nil {
		s.logger.Error("list share links failed", "deck_id", deck.ID, "error", err)
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

	now := time.Now().UTC()
	linkRows := make([]views.ShareLinkRow, 0, len(links))
	for i := range links {
		l := links[i]
		active := l.Active(now)
		state := loc.T("sharing.links.state_active")
		switch {
		case l.RevokedAt != nil:
			state = loc.T("sharing.links.state_revoked")
		case l.ExpiresAt != nil && !now.Before(*l.ExpiresAt):
			state = loc.T("sharing.links.state_expired")
		}
		password := loc.T("sharing.links.password_no")
		if l.PasswordHash != nil {
			password = loc.T("sharing.links.password_yes")
		}
		expires := loc.T("sharing.links.no_expiry")
		if l.ExpiresAt != nil {
			expires = l.ExpiresAt.UTC().Format("2006-01-02 15:04")
		}
		linkRows = append(linkRows, views.ShareLinkRow{
			TokenValue:   l.Token,
			Prefix:       shortDigest(l.Token),
			CreatedText:  l.CreatedAt.UTC().Format("2006-01-02 15:04"),
			ExpiresText:  expires,
			PasswordText: password,
			StateText:    state,
			Active:       active,
			RevokeLabel:  loc.T("sharing.links.revoke_submit"),
			RevokeAction: fmt.Sprintf("/decks/%d/sharing/links/revoke", deck.ID),
		})
	}
	shownLink := ""
	if newLinkURL != "" {
		shownLink = "/s/" + newLinkURL
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
		// M5-3 分享链接区块。
		LinksHeading:     loc.T("sharing.links.heading"),
		LinksEmpty:       loc.T("sharing.links.empty"),
		ColLink:          loc.T("sharing.links.col_link"),
		ColPassword:      loc.T("sharing.links.col_password"),
		ColExpires:       loc.T("sharing.links.col_expires"),
		ColState:         loc.T("sharing.links.col_state"),
		LinkRows:         linkRows,
		CreateLinkAction: fmt.Sprintf("/decks/%d/sharing/links", deck.ID),
		PasswordLabel:    loc.T("sharing.links.password_label"),
		PasswordHint:     loc.T("sharing.links.password_hint"),
		ExpiresLabel:     loc.T("sharing.links.expires_label"),
		ExpiresHint:      loc.T("sharing.links.expires_hint"),
		CreateLinkLabel:  loc.T("sharing.links.create_submit"),
		NewLinkURL:       shownLink,
		NewLinkNotice:    loc.T("sharing.links.created_notice"),
		RevokeAllAction:  fmt.Sprintf("/decks/%d/sharing/links/revoke-all", deck.ID),
		RevokeAllLabel:   loc.T("sharing.links.revoke_all_submit"),
		// M5-5 可见性区块。
		VisibilityHeading: loc.T("sharing.visibility.heading"),
		VisibilityLabel:   loc.T("sharing.visibility.label"),
		VisibilityNote:    loc.T("sharing.visibility.note"),
		VisibilityOptions: s.visibilityOptions(loc, deck.Visibility),
		VisibilitySubmit:  loc.T("sharing.visibility.submit"),
		VisibilityAction:  fmt.Sprintf("/decks/%d/sharing/visibility", deck.ID),
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

// visibilityOptions 生成可见性下拉（M5-5）。空串（M2 老行）按 private 展示，与实际语义一致。
func (s *Server) visibilityOptions(loc *i18n.Localizer, current string) []views.RoleOption {
	if current == "" {
		current = store.DeckVisibilityPrivate
	}
	values := []string{store.DeckVisibilityPrivate, store.DeckVisibilityUnlisted, store.DeckVisibilityPublic}
	out := make([]views.RoleOption, 0, len(values))
	for _, v := range values {
		out = append(out, views.RoleOption{
			Value:    v,
			Label:    loc.T("sharing.visibility." + v),
			Selected: v == current,
		})
	}
	return out
}

// shortDigest 截断 token 摘要用于列表展示；摘要不可反推，展示前缀不构成密钥泄漏。
func shortDigest(digest string) string {
	if len(digest) <= 12 {
		return digest
	}
	return digest[:12] + "…"
}

// shareLinkPasswordHasher 是分享链接口令的哈希器。
// 口令是“给人转发时顺手加的一道锁”，不是账号主密码，因此复用项目统一的 argon2id 编码格式，
// 参数从 DefaultParams 起步；将来升级强度只影响新口令，旧哈希自带参数仍可验证。
var shareLinkPasswordHasher = auth.DefaultPasswordHasher()

// sharingLinkCreate 创建分享链接（仅 owner）：可选口令、可选过期。
// 成功后把明文链接就地渲染一次 —— 库里只有摘要，错过这一次就只能重新创建（DESIGN.md §11）。
func (s *Server) sharingLinkCreate(c *gin.Context) {
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

	var passwordHash *string
	if raw := c.PostForm("password"); strings.TrimSpace(raw) != "" {
		pw := strings.TrimSpace(raw)
		if len(pw) > auth.MaxPasswordLength {
			s.renderSharing(c, loc, deck, http.StatusBadRequest, loc.T("sharing.links.error_password_too_long"))
			return
		}
		hash, err := shareLinkPasswordHasher.Hash(pw)
		if err != nil {
			s.logger.Error("hash share link password failed", "deck_id", deck.ID, "error", err)
			s.renderSharing(c, loc, deck, http.StatusInternalServerError, loc.T("sharing.links.error_create_failed"))
			return
		}
		passwordHash = &hash
	}

	var expiresAt *time.Time
	expiresDetail := ""
	if raw := strings.TrimSpace(c.PostForm("expires_at")); raw != "" {
		day, err := time.Parse("2006-01-02", raw)
		if err != nil {
			s.renderSharing(c, loc, deck, http.StatusBadRequest, loc.T("sharing.links.error_expiry_invalid"))
			return
		}
		// 选中的日期按“当天结束”生效，避免选了今天却立刻过期。
		end := day.AddDate(0, 0, 1)
		expiresAt = &end
		expiresDetail = end.UTC().Format(time.RFC3339)
	}

	plaintext, _, err := s.shareLinks.Create(ctx, store.ShareLinkInput{
		DeckID:       deck.ID,
		PasswordHash: passwordHash,
		ExpiresAt:    expiresAt,
		CreatedBy:    user.ID,
	})
	if err != nil {
		s.logger.Error("create share link failed", "deck_id", deck.ID, "error", err)
		s.renderSharing(c, loc, deck, http.StatusInternalServerError, loc.T("sharing.links.error_create_failed"))
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionShareLinkCreate,
		TargetType: "deck",
		TargetID:   store.Ptr(deck.ID),
		Detail:     map[string]any{"has_password": passwordHash != nil, "expires_at": expiresDetail},
	})
	s.renderSharingPage(c, loc, deck, http.StatusOK, "", plaintext)
}

// sharingLinkRevoke 撤销单个分享链接（仅 owner）；表单带的是摘要而不是明文。
func (s *Server) sharingLinkRevoke(c *gin.Context) {
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
	digest := strings.TrimSpace(c.PostForm("link"))
	if digest == "" {
		s.renderSharing(c, loc, deck, http.StatusBadRequest, loc.T("sharing.links.error_link_required"))
		return
	}
	ctx := c.Request.Context()
	if err := s.shareLinks.Revoke(ctx, deck.ID, digest); err != nil {
		s.logger.Error("revoke share link failed", "deck_id", deck.ID, "error", err)
		s.renderSharing(c, loc, deck, http.StatusInternalServerError, loc.T("sharing.links.error_revoke_failed"))
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionShareLinkRevoke,
		TargetType: "deck",
		TargetID:   store.Ptr(deck.ID),
		Detail:     map[string]any{"link": shortDigest(digest)},
	})
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/decks/%d/sharing", deck.ID))
}

// sharingLinkRevokeAll 一次撤销该卡组的全部有效链接（仅 owner）。
func (s *Server) sharingLinkRevokeAll(c *gin.Context) {
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
	n, err := s.shareLinks.RevokeAll(ctx, deck.ID)
	if err != nil {
		s.logger.Error("revoke all share links failed", "deck_id", deck.ID, "error", err)
		s.renderSharing(c, loc, deck, http.StatusInternalServerError, loc.T("sharing.links.error_revoke_failed"))
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionShareLinkRevokeAll,
		TargetType: "deck",
		TargetID:   store.Ptr(deck.ID),
		Detail:     map[string]any{"revoked": n},
	})
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/decks/%d/sharing", deck.ID))
}

// sharingVisibility 修改卡组可见性（仅 owner，M5-5）。
// 变化才写库与审计：重复提交同一个值是无操作，与授权路径的幂等处理一致。
func (s *Server) sharingVisibility(c *gin.Context) {
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
	visibility := strings.TrimSpace(c.PostForm("visibility"))
	if err := s.decks.SetVisibility(c.Request.Context(), user.ID, deck.ID, visibility); err != nil {
		if errors.Is(err, store.ErrInvalidVisibility) {
			s.renderSharing(c, loc, deck, http.StatusBadRequest, loc.T("sharing.visibility.error_invalid"))
			return
		}
		s.logger.Error("set deck visibility failed", "deck_id", deck.ID, "error", err)
		s.renderSharing(c, loc, deck, http.StatusInternalServerError, loc.T("sharing.visibility.error_failed"))
		return
	}
	if visibility != deck.Visibility {
		s.audit(c.Request.Context(), store.AuditEntry{
			UserID:     store.Ptr(user.ID),
			Action:     store.ActionDeckVisibility,
			TargetType: "deck",
			TargetID:   store.Ptr(deck.ID),
			Detail:     map[string]any{"previous": deck.Visibility, "visibility": visibility},
		})
	}
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/decks/%d/sharing", deck.ID))
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
