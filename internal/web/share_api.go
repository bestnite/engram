package web

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是公开只读分享浏览的 SPA 同源 JSON 传输层与 GET 切壳。
//
// 安全边界与迁移前逐项一致，绝不因切壳放宽或收紧：
//   - 撤销、过期或不存在一律 404，不区分三者（不泄漏 token 历史）；
//   - 有口令的链接在解锁前不返回任何卡片内容，只给「需要口令 + 卡组名」；
//   - 卡片正反面一律复用服务端清洗渲染管线（shareNoteView → renderSide），客户端只把清洗后的
//     HTML 交给 {@html}，绝不把 fields 原文当可信 Markdown 渲染；
//   - 打开成功时登记会话级媒体读取授权（recordShareGrant），媒体可读性仍由 /media 的登录门禁决定。
//
// 匿名访问的现状不变：分享链接本身仍是凭据，GET /s/:token 从不要求登录，这里也不要求。

// apiShareNote 是一条共享卡片的正反面清洗后 HTML。
type apiShareNote struct {
	FrontHTML string `json:"front_html"`
	BackHTML  string `json:"back_html"`
}

// apiShareResponse 是分享浏览内容的 JSON 形态。
// password_required 为真时 notes 恒为空，绝不返回解锁前的内容。
type apiShareResponse struct {
	DeckName         string         `json:"deck_name"`
	PasswordRequired bool           `json:"password_required"`
	Notes            []apiShareNote `json:"notes"`
}

// registerShareRoutes 挂载分享浏览的 SPA JSON 端点；口令解锁走免 CSRF 的 POST（与 SSR 同一理由：
// 此刻没有服务端会话可绑定 token，且除渲染内容外不产生状态）。
func (s *Server) registerShareRoutes(router *gin.Engine) {
	if s.shareLinks == nil || s.decks == nil || s.notes == nil {
		return
	}
	router.GET("/api/v1/share/:token", s.shareGet)
	router.POST("/api/v1/share/:token/unlock", s.shareUnlock)
	// 加入卡组会写授权行（状态变更）且只对已登录用户开放，因此过 CSRF 中间件。
	if s.sessions != nil {
		router.POST("/api/v1/share/:token/join", s.sessions.CSRFMiddleware(), s.shareJoin)
	}
}

// shareBrowseRoute 提供 GET /s/:token：返回应用壳，由客户端路由渲染只读浏览页；
// 内容走 GET /api/v1/share/:token（同一份清洗渲染与媒体授权）。
//
// 可达性判定必须留在切壳之前：撤销、过期或不存在的链接仍返回 404，与该 URL 迁移前的行为一致。
func (s *Server) shareBrowseRoute(c *gin.Context) {
	if _, _, ok := s.resolveShareLink(c); !ok {
		return
	}
	s.shell.ServeIndex(c)
}

// shareGet 返回一份分享卡组的只读内容（GET /api/v1/share/:token）。
//
// 无口令链接直接返回内容；有口令的链接在解锁前只返回 {password_required: true}，不泄漏卡片正文。
func (s *Server) shareGet(c *gin.Context) {
	link, deck, ok := s.resolveShareLink(c)
	if !ok {
		return
	}
	if link.PasswordHash != nil {
		c.JSON(http.StatusOK, apiShareResponse{
			DeckName:         deck.Name,
			PasswordRequired: true,
			Notes:            []apiShareNote{},
		})
		return
	}
	s.shareContent(c, deck, link)
}

// shareUnlock 校验口令并返回内容（POST /api/v1/share/:token/unlock）。
//
// 无口令链接等价于直接浏览（幂等）；口令错误返回 401 与稳定 code。
// 这里刻意不消费任何令牌、不建立解锁状态——每次请求都重新校验，因此无状态。
func (s *Server) shareUnlock(c *gin.Context) {
	var req struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiAuthError(c, http.StatusBadRequest, api.CodeInvalidRequest, "The request is invalid.")
		return
	}
	link, deck, ok := s.resolveShareLink(c)
	if !ok {
		return
	}
	if link.PasswordHash == nil {
		s.shareContent(c, deck, link)
		return
	}
	matched, err := auth.Verify(*link.PasswordHash, req.Password)
	if err != nil {
		s.logger.Error("verify share link password failed", "deck_id", deck.ID, "error", err)
		apiAuthError(c, http.StatusUnauthorized, "share_password_invalid", "The password is incorrect.")
		return
	}
	if !matched {
		s.logger.Info("share link password rejected", "deck_id", deck.ID)
		apiAuthError(c, http.StatusUnauthorized, "share_password_invalid", "The password is incorrect.")
		return
	}
	s.shareContent(c, deck, link)
}

// shareContent 组装只读内容的 JSON：登记会话级媒体授权后列出卡片并清洗渲染。
// 单张坏卡跳过并记英文日志，不阻断其余内容的展示。
func (s *Server) shareContent(c *gin.Context, deck *store.Deck, link *store.ShareLink) {
	ctx := c.Request.Context()
	// 访问已经成功（无口令，或口令校验通过）→ 登记「本会话打开过这个卡组」，
	// 浏览器随后对 /media/<sha256> 的请求才放行；匿名访客没有服务端会话，直接跳过。
	s.recordShareGrant(c, deck, link)
	notes, _, err := s.notes.List(ctx, store.NoteListOptions{
		DeckID: deck.ID, Page: 1, PerPage: store.DefaultNotePageSize, Status: store.NoteStatusActive,
	})
	if err != nil {
		s.logger.Error("list notes for share browse failed", "deck_id", deck.ID, "error", err)
		apiAuthError(c, http.StatusInternalServerError, api.CodeInternal, "An internal error occurred.")
		return
	}
	rendered := make([]apiShareNote, 0, len(notes))
	for i := range notes {
		front, back, err := s.shareNoteView(notes[i])
		if err != nil {
			// 单张坏卡不阻断浏览：跳过并记英文日志，其余内容照常展示。
			s.logger.Error("render shared note failed", "note_id", notes[i].ID, "error", err)
			continue
		}
		rendered = append(rendered, apiShareNote{FrontHTML: front, BackHTML: back})
	}
	c.JSON(http.StatusOK, apiShareResponse{
		DeckName:         deck.Name,
		PasswordRequired: false,
		Notes:            rendered,
	})
}

// shareJoin 让「持链接的已登录用户」把这个卡组加进自己的列表（POST /api/v1/share/:token/join）。
//
// 为什么入伙要一次显式动作，而不是打开链接就自动入伙：链接会被转发、会在群里被随手点开，
// 而「打开一次」只等于同意看一次，不等于同意把一个卡组塞进我的列表与复习队列——进了列表
// 就会占我的每日新卡额度。因此打开链接只登记会话级只读（recordShareGrant，与链接同生死），
// 入伙由用户自己按一次按钮决定；token 就是这次入伙的凭据，没有链接的人拿不到授权。
//
// 入伙之后这条授权与链接无关（撤销/过期链接不回收它），属主要在共享页逐个撤销成员——
// 与「邀请被接受」得到的授权完全同一种，撤销路径也同一条。
//
// 失败语义：链接已撤销/已过期/不存在 → 404（不区分三者，不泄漏 token 历史）；
// 未登录 → 401（前端把未登录访客引导去登录，不显示这个按钮）；属主自己按、或已有授权
// （含 editor）→ 幂等成功，既不写多余授权行，也不把角色降级成 reader。
func (s *Server) shareJoin(c *gin.Context) {
	if s.grants == nil {
		apiAuthError(c, http.StatusInternalServerError, api.CodeInternal, "An internal error occurred.")
		return
	}
	user, ok := auth.CurrentUser(c)
	if !ok || user == nil {
		apiAuthError(c, http.StatusUnauthorized, api.CodeUnauthorized, "Authentication is required.")
		return
	}
	// 可达性判定与浏览页同源：撤销/过期/不存在一律 404。
	_, deck, ok := s.resolveShareLink(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	joinable := deck.OwnerUserID != user.ID
	if joinable {
		existing, err := s.grants.Role(ctx, deck.ID, user.ID)
		if err != nil {
			s.logger.Error("read deck grant for share join failed", "deck_id", deck.ID, "user_id", user.ID, "error", err)
			apiAuthError(c, http.StatusInternalServerError, api.CodeInternal, "An internal error occurred.")
			return
		}
		joinable = !store.ValidRole(existing)
	}
	if joinable {
		if err := s.grants.Grant(ctx, deck.ID, user.ID, store.RoleReader, nil); err != nil {
			s.logger.Error("join deck via share link failed", "deck_id", deck.ID, "user_id", user.ID, "error", err)
			apiAuthError(c, http.StatusInternalServerError, api.CodeInternal, "An internal error occurred.")
			return
		}
		s.audit(ctx, store.AuditEntry{
			UserID:     store.Ptr(user.ID),
			Action:     store.ActionShareLinkJoin,
			TargetType: "deck",
			TargetID:   store.Ptr(deck.ID),
			Detail:     map[string]any{"role": store.RoleReader},
		})
	}
	c.JSON(http.StatusOK, gin.H{"deck_id": deck.ID, "joined": true})
}
