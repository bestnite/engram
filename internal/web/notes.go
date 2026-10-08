package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/cardtype"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/render"
	"git.nite07.com/nite/engram/internal/store"
)

// registerNoteRoutes 挂载卡片列表与编辑页。
//
// 依赖未装配时跳过，保证 M0 阶段的测试仍能构造 Server。所有权判断先用 deck owner
// （deck_grants 尚未落地，会把它扩展成 owner/editor 判定）。
func (s *Server) registerNoteRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.notes == nil {
		return
	}
	// 卡片列表、编辑与新建三个 GET 页都返回应用壳：三个 *Route 处理器
	// 先按各自原有的会话与角色判定鉴权，再交出应用壳，由客户端路由与 JSON 端点渲染页面。
	router.GET("/decks/:id/notes", s.noteListRoute)
	router.GET("/decks/:id/notes/:nid", s.noteEditRoute)
	// 新建卡片：单独的路径前缀，避免与 /notes/:nid 的参数路由产生歧义。
	router.GET("/decks/:id/new-note", s.noteNewRoute)
	// SPA 的新建/编辑预览走 JSON。
	router.POST("/api/v1/decks/:id/notes/preview", s.sessions.CSRFMiddleware(), s.notePreview)
}

// requireUser 取当前登录用户；未登录时重定向到登录页并返回 false。
func (s *Server) requireUser(c *gin.Context) (*store.User, bool) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		c.Redirect(http.StatusSeeOther, "/login")
		return nil, false
	}
	return u, true
}

// deckIDParam 解析 :id 路径参数；解析失败时返回 false 并写 404。
func deckIDParam(c *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.AbortWithStatus(http.StatusNotFound)
		return 0, false
	}
	return id, true
}

// loadDeckForRole 取卡组并校验当前用户至少拥有 want 角色。
//
// 判定本体在 auth.DeckAccess（与 REST/MCP 共用同一实现，不复制第二份）；这里只负责
// 把错误映射成 HTML 响应：卡组不存在 -> 404，权限不足 -> 403，并写一条 permission.denied
// 审计（谁在什么时候想对哪个卡组做什么被挡下）。
func (s *Server) loadDeckForRole(c *gin.Context, user *store.User, deckID uint64, want string) (*store.Deck, bool) {
	if s.access == nil {
		// 装配缺失属于服务端配置问题，不能放行。
		s.logger.Error("deck access checker is not wired", "deck_id", deckID)
		c.AbortWithStatus(http.StatusInternalServerError)
		return nil, false
	}
	deck, role, err := s.access.RequireRole(c.Request.Context(), deckID, user.ID, want)
	if err == nil {
		return deck, true
	}
	if errors.Is(err, auth.ErrDeckNotFound) {
		c.AbortWithStatus(http.StatusNotFound)
		return nil, false
	}
	s.audit(c.Request.Context(), store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionPermissionDenied,
		TargetType: "deck",
		TargetID:   store.Ptr(deckID),
		Detail:     map[string]any{"required_role": want, "user_role": role},
	})
	c.AbortWithStatus(http.StatusForbidden)
	return nil, false
}

// parsePage 解析 ?page=；非法或缺失时按第 1 页处理。
func parsePage(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// noteListRoute 提供 GET /decks/:id/notes：返回应用壳，由客户端路由渲染
// 卡片列表，数据仍走既有 JSON 端点（GET /api/v1/decks/:id/notes）。
//
// 鉴权与迁移前的 SSR 列表页逐项一致：先要求已登录会话（匿名重定向登录页），再按 reader 角色
// 判定卡组可读性——无权读的卡组仍回 403，不因切壳而放行。
func (s *Server) noteListRoute(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	deckID, ok := deckIDParam(c)
	if !ok {
		return
	}
	if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleReader); !ok {
		return
	}
	s.shell.ServeIndex(c)
}

// noteEditRoute 提供 GET /decks/:id/notes/:nid：返回应用壳，由客户端路由
// 渲染卡片编辑页，数据仍走既有 JSON 端点（GET 列表 + PATCH /api/v1/notes/:id）。
// 编辑页的写入在 SPA 里走 REST，但读页面的判权仍由服务端负责。
//
// 鉴权与迁移前的 SSR 编辑页逐项一致：先要求已登录会话（匿名重定向登录页），再按 editor 角色
// 判定卡组可写性——非 editor 仍回 403，不因切壳而把编辑壳交给无权用户。
func (s *Server) noteEditRoute(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	deckID, ok := deckIDParam(c)
	if !ok {
		return
	}
	if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleEditor); !ok {
		return
	}
	s.shell.ServeIndex(c)
}

// noteNewRoute 提供 GET /decks/:id/new-note：返回应用壳，由客户端路由渲染新建卡片页，
// 提交仍走 POST /api/v1/decks/:id/notes。
//
// 鉴权与迁移前的 SSR 新建页逐项一致：匿名重定向登录页，非 editor 回 403。
func (s *Server) noteNewRoute(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	deckID, ok := deckIDParam(c)
	if !ok {
		return
	}
	if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleEditor); !ok {
		return
	}
	s.shell.ServeIndex(c)
}

// PreviewCard 是一张卡的正反面渲染结果。
type PreviewCard struct {
	TemplateLabel string
	FrontHTML     string
	BackHTML      string
}

// NotePreviewData 是卡片编辑页预览的载荷。
type NotePreviewData struct {
	Title      string
	FrontLabel string
	BackLabel  string
	Error      string
	Cards      []PreviewCard
}

// buildPreview 用 internal/render 渲染每个 card 的正反面，供 SPA 预览 JSON 使用。
func (s *Server) buildPreview(loc *i18n.Localizer, kind string, fields map[string]any) NotePreviewData {
	data := NotePreviewData{
		Title:      loc.T("notes.preview.title"),
		FrontLabel: loc.T("notes.preview.front"),
		BackLabel:  loc.T("notes.preview.back"),
	}
	t, ok := cardtype.Lookup(kind)
	if !ok {
		data.Error = loc.T("notes.preview.invalid") + kind
		return data
	}
	cards, err := cardtype.Cards(cardtype.Note{Kind: kind, Fields: fields})
	if err != nil {
		data.Error = loc.T("notes.preview.invalid") + err.Error()
		return data
	}
	for _, card := range cards {
		front, err := t.Render(card, cardtype.SideFront)
		if err != nil {
			data.Error = loc.T("notes.preview.invalid") + err.Error()
			return data
		}
		back, err := t.Render(card, cardtype.SideBack)
		if err != nil {
			data.Error = loc.T("notes.preview.invalid") + err.Error()
			return data
		}
		frontHTML, err := renderSide(front)
		if err != nil {
			data.Error = loc.T("notes.preview.invalid") + err.Error()
			return data
		}
		backHTML, err := renderSide(back)
		if err != nil {
			data.Error = loc.T("notes.preview.invalid") + err.Error()
			return data
		}
		data.Cards = append(data.Cards, PreviewCard{
			TemplateLabel: card.Template,
			FrontHTML:     frontHTML,
			BackHTML:      backHTML,
		})
	}
	return data
}

// renderSide 把一侧的 Markdown 文本（含 Extra 片段）渲染成清洗后的 HTML。
func renderSide(side cardtype.RenderResult) (string, error) {
	body := side.Body
	if len(side.Extra) > 0 {
		body = strings.Join(append([]string{body}, side.Extra...), "\n\n")
	}
	return render.RenderMarkdown(body)
}
