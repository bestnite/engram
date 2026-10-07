package web

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是公开只读分享浏览（M5-3）的 SPA 同源 JSON 传输层与 GET 切壳。
//
// 安全边界与迁移前逐项一致，绝不因切壳放宽或收紧：
//   - 撤销、过期或不存在一律 404，不区分三者（不泄漏 token 历史）；
//   - 有口令的链接在解锁前不返回任何卡片内容，只给「需要口令 + 卡组名」（与 SSR 的口令页一致）；
//   - 卡片正反面一律复用服务端清洗渲染管线（shareNoteView → renderSide），客户端只把清洗后的
//     HTML 交给 {@html}，绝不把 fields 原文当可信 Markdown 渲染；
//   - 打开成功时登记会话级媒体读取授权（recordShareGrant），媒体可读性仍由 /media 的登录门禁决定。
//
// 匿名访问的现状不变：分享链接本身仍是凭据，SSR 的 GET /s/:token 从不要求登录，这里也不要求。

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

// registerSPAShareRoutes 挂载分享浏览的 SPA JSON 端点；口令解锁走免 CSRF 的 POST（与 SSR 同一理由：
// 此刻没有服务端会话可绑定 token，且除渲染内容外不产生状态）。
func (s *Server) registerSPAShareRoutes(router *gin.Engine) {
	if s.shareLinks == nil || s.decks == nil || s.notes == nil {
		return
	}
	router.GET("/api/v1/share/:token", s.spaShareGet)
	router.POST("/api/v1/share/:token/unlock", s.spaShareUnlock)
}

// shareBrowseRoute 提供 GET /s/:token：返回应用壳，由客户端路由渲染只读浏览页；
// 内容走 GET /api/v1/share/:token（同一份清洗渲染与媒体授权）。
//
// 可达性判定必须留在切壳之前：撤销、过期或不存在的链接仍返回 404，与该 URL 迁移前的行为一致。
func (s *Server) shareBrowseRoute(c *gin.Context) {
	if _, _, ok := s.resolveShareLink(c); !ok {
		return
	}
	s.spa.ServeIndex(c)
}

// spaShareGet 返回一份分享卡组的只读内容（GET /api/v1/share/:token）。
//
// 无口令链接直接返回内容；有口令的链接在解锁前只返回 {password_required: true}，不泄漏卡片正文。
func (s *Server) spaShareGet(c *gin.Context) {
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
	s.spaShareContent(c, deck, link)
}

// spaShareUnlock 校验口令并返回内容（POST /api/v1/share/:token/unlock）。
//
// 与 SSR 的 shareUnlock 同语义：无口令链接等价于直接浏览（幂等）；口令错误返回 401 与稳定 code。
// 这里刻意不消费任何令牌、不建立解锁状态——每次请求都重新校验，与 SSR 一样无状态。
func (s *Server) spaShareUnlock(c *gin.Context) {
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
		s.spaShareContent(c, deck, link)
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
	s.spaShareContent(c, deck, link)
}

// spaShareContent 组装只读内容的 JSON：登记会话级媒体授权后列出卡片并清洗渲染。
// 与 SSR 的 renderShareContent 同源：单张坏卡跳过并记英文日志，不阻断其余内容的展示。
func (s *Server) spaShareContent(c *gin.Context, deck *store.Deck, link *store.ShareLink) {
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
