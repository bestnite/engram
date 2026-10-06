package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/cardtype"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/render"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
)

// builtinFieldKeys 是有语言包条目的字段名；其它字段名直接显示原始键名，
// 避免为未知字段（可能来自将来的题型）产生缺失键日志（DESIGN.md §6.2）。
var builtinFieldKeys = map[string]bool{
	"front": true, "back": true, "text": true, "prompt": true,
	"items": true, "ordered": true, "extra": true, "source_url": true,
	// M2-10：作答类与简答题的字段名（DESIGN.md §6.2 冻结表）。
	"question": true, "answer": true, "accept": true, "options": true, "answers": true,
	"statement": true, "value": true, "unit": true,
	"tolerance_absolute": true, "tolerance_relative": true,
	"ignore_case": true, "ignore_whitespace": true, "regex": true, "reference": true,
}

// fieldOrder 决定编辑页字段输入框的展示顺序；不在其中的字段追加在后面。
var fieldOrder = []string{
	"front", "back", "text", "prompt", "items", "ordered", "extra", "source_url",
	"question", "options", "answer", "answers", "accept",
	"statement", "value", "unit", "tolerance_absolute", "tolerance_relative",
	"ignore_case", "ignore_whitespace", "regex", "reference",
}

// summaryFieldOrder 是列表“正面摘要”的取值优先级，覆盖内置题型的题干字段。
var summaryFieldOrder = []string{"front", "prompt", "question", "statement", "text"}

// registerNoteRoutes 挂载卡片列表与编辑页（M2-7）。
//
// 依赖未装配时跳过，保证 M0 阶段的测试仍能构造 Server。所有权判断先用 deck owner
// （deck_grants 尚未落地，M5-1 会把它扩展成 owner/editor 判定）。
func (s *Server) registerNoteRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.notes == nil {
		return
	}
	// 卡片列表、编辑与新建三个 GET 页都已切到 SPA 规范路径：三个 *Route 处理器先按各自 SSR
	// 页原有的会话与角色判定鉴权，再返回应用壳（DESIGN.md §8.1、§8.5）。SPA 缺失（降级）时
	// 各自回退到对应的 SSR 页面（noteList / noteEdit / noteNew），旧页面与模板全部保留。
	router.GET("/decks/:id/notes", s.noteListRoute)
	router.GET("/decks/:id/notes/:nid", s.noteEditRoute)
	// M2-12 新建卡片：单独的路径前缀，避免与 /notes/:nid 的参数路由产生歧义。
	router.GET("/decks/:id/new-note", s.noteNewRoute)
	router.GET("/decks/:id/new-note/fields", s.noteFieldsFragment)
	// GET 之外的写操作一律过 CSRF 中间件（DESIGN.md §4.3、§11）。
	router.POST("/decks/:id/notes/:nid", s.sessions.CSRFMiddleware(), s.noteUpdate)
	router.POST("/decks/:id/notes", s.sessions.CSRFMiddleware(), s.noteCreate)
	router.POST("/decks/:id/preview", s.sessions.CSRFMiddleware(), s.notePreview)
	router.POST("/decks/:id/preview-new", s.sessions.CSRFMiddleware(), s.noteCreatePreview)
	router.POST("/api/v1/decks/:id/notes/preview", s.sessions.CSRFMiddleware(), s.spaNotePreview)
	router.POST("/decks/:id/bulk", s.sessions.CSRFMiddleware(), s.noteBulk)
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

// pageLayout 构造普通页面（列表/编辑）的外壳数据；文案全部取自语言包。
// 顶部导航走全站唯一构造器 mainNav（M8-7），active 取当前请求路径。
func (s *Server) pageLayout(c *gin.Context, loc *i18n.Localizer, titleKey string) views.LayoutData {
	layout := views.LayoutData{
		Lang:       loc.Locale(),
		Title:      loc.T(titleKey),
		Brand:      loc.T("app.name"),
		HomeURL:    "/",
		CSSURL:     s.assets.URL("css/tailwind.css"),
		HTMXURL:    s.assets.URL("js/htmx.min.js"),
		MathJaxURL: s.assets.URL("js/mathjax/tex-svg.js"),
		MediaJSURL: s.assets.URL("js/media.js"),
		NotesJSURL: s.assets.URL("js/notes.js"),
	}
	// 语言切换下拉、页脚与哈希化图标对所有页面外壳一致。
	s.decorateLayout(c, loc, &layout)
	layout.Nav = s.mainNav(c, loc, c.Request.URL.Path)
	layout.SessionLabel = loc.T("nav.login")
	layout.SessionHref = "/login"
	if _, ok := auth.CurrentUser(c); ok {
		layout.SessionLabel = loc.T("nav.logout")
		layout.SessionHref = "/logout"
		layout.SessionForm = true
		if sess, ok := auth.CurrentSession(c); ok {
			layout.CSRF = sess.CSRFToken
		}
	}
	return layout
}

// mediaUploadData 构造编辑器里的媒体上传控件数据（M2-9）。存储未装配时返回 Disabled，
// 模板据此不渲染控件。accept 取自当前生效的 mime 白名单，避免在模板里硬编码格式列表；
// 失败文案按稳定英文 code（media_too_large 等）分档，由前端 media.js 选择。
func (s *Server) mediaUploadData(c *gin.Context, loc *i18n.Localizer, deckID uint64) views.MediaUploadData {
	if s.media == nil {
		return views.MediaUploadData{Disabled: true}
	}
	return views.MediaUploadData{
		URL:         fmt.Sprintf("/decks/%d/media", deckID),
		Accept:      strings.Join(s.allowedMimes(c.Request.Context()), ","),
		Label:       loc.T("media.upload.label"),
		Button:      loc.T("media.upload.button"),
		Hint:        loc.T("media.upload.hint"),
		Inserted:    loc.T("media.upload.inserted"),
		ErrTooLarge: loc.T("media.upload.error.too_large"),
		ErrMime:     loc.T("media.upload.error.mime"),
		ErrMagic:    loc.T("media.upload.error.missing_magic"),
		ErrGeneric:  loc.T("media.upload.error.generic"),
	}
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

// loadDeckForRole 取卡组并校验当前用户至少拥有 want 角色（M5-1）。
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

// loadOwnedDeck 保留旧名，语义收敛为“至少能看”（reader 及以上）。
// 涉及写入的 handler 必须改用 loadDeckForRole(..., store.RoleEditor)。
func (s *Server) loadOwnedDeck(c *gin.Context, user *store.User, deckID uint64) (*store.Deck, bool) {
	return s.loadDeckForRole(c, user, deckID, store.RoleReader)
}

// kindLabel 把 kind 映射成语言包里的显示名；未知 kind 回退成原始标识。
func (s *Server) kindLabel(loc *i18n.Localizer, kind string) string {
	if t, ok := cardtype.Lookup(kind); ok {
		return loc.T(t.Label())
	}
	return kind
}

// fieldLabel 返回字段的本地化标签；内置字段走语言包，其它字段用原始键名。
func (s *Server) fieldLabel(loc *i18n.Localizer, name string) string {
	if builtinFieldKeys[name] {
		return loc.T("note.field." + name)
	}
	return name
}

// noteSummary 从字段中取列表摘要（优先题干类字段），截断到有限长度。
func noteSummary(fields map[string]any) string {
	for _, key := range summaryFieldOrder {
		if v, ok := fields[key].(string); ok && strings.TrimSpace(v) != "" {
			return truncateRunes(strings.TrimSpace(v), 80)
		}
	}
	// 没有题干类字段时退回第一个字符串字段，保证列表不空白。
	for _, key := range fieldOrder {
		if v, ok := fields[key].(string); ok && strings.TrimSpace(v) != "" {
			return truncateRunes(strings.TrimSpace(v), 80)
		}
	}
	return ""
}

// truncateRunes 按 Unicode 字符截断，避免把多字节字符切成半个。
func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}

// isMediaNotReadable 判断写入失败是否由写前媒体校验引起（store.MediaWriteError），
// 供 handler 选择与「字段非法」不同的本地化文案。
func isMediaNotReadable(err error) bool {
	var mwe *store.MediaWriteError
	return errors.As(err, &mwe)
}

// parsePage 解析 ?page=；非法或缺失时按第 1 页处理。
func parsePage(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// normalizeStatus 把 ?status= 收窄到三个合法取值之一。
func normalizeStatus(raw string) string {
	switch strings.TrimSpace(raw) {
	case store.NoteStatusDeleted:
		return store.NoteStatusDeleted
	case store.NoteStatusAll:
		return store.NoteStatusAll
	default:
		return store.NoteStatusActive
	}
}

// noteListHref 构造保留筛选条件的列表地址。
func noteListHref(deckID uint64, page int, q, tag, kind, status string) string {
	values := []string{}
	if q != "" {
		values = append(values, "q="+urlEncode(q))
	}
	if tag != "" {
		values = append(values, "tag="+urlEncode(tag))
	}
	if kind != "" {
		values = append(values, "kind="+urlEncode(kind))
	}
	if status != "" && status != store.NoteStatusActive {
		values = append(values, "status="+urlEncode(status))
	}
	if page > 1 {
		values = append(values, "page="+strconv.Itoa(page))
	}
	base := fmt.Sprintf("/decks/%d/notes", deckID)
	if len(values) == 0 {
		return base
	}
	return base + "?" + strings.Join(values, "&")
}

// urlEncode 对查询参数做最小转义。
func urlEncode(s string) string {
	r := strings.NewReplacer(" ", "%20", "&", "%26", "?", "%3F", "#", "%23", "+", "%2B", "=", "%3D", "%", "%25")
	return r.Replace(s)
}

// noteListRoute 提供 GET /decks/:id/notes：SPA 已加载时返回应用壳（DESIGN.md §8.5），由客户端
// 路由渲染卡片列表，数据仍走既有 JSON 端点（DESIGN.md §8.1）。
//
// 鉴权与迁移前的 SSR 列表页逐项一致：先要求已登录会话（匿名重定向登录页），再按 reader 角色
// 判定卡组可读性——无权读的卡组仍回 403，不因切壳而放行。SPA 缺失（降级）时回退 SSR 列表页。
// 卡片编辑（/decks/:id/notes/:nid）与新建（/decks/:id/new-note）同样已切壳，见 noteEditRoute
// 与 noteNewRoute。
func (s *Server) noteListRoute(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	if s.spa != nil {
		deckID, ok := deckIDParam(c)
		if !ok {
			return
		}
		if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleReader); !ok {
			return
		}
		s.spa.ServeIndex(c)
		return
	}
	s.noteList(c)
}

// noteEditRoute 提供 GET /decks/:id/notes/:nid：SPA 已加载时返回应用壳（DESIGN.md §8.5），由
// 客户端路由渲染卡片编辑页，数据仍走既有 JSON 端点（GET 列表 + PATCH /api/v1/notes/:id，
// DESIGN.md §8.1）。编辑页的写入在 SPA 里走 REST，但读页面的判权仍由服务端负责。
//
// 鉴权与迁移前的 SSR 编辑页逐项一致：先要求已登录会话（匿名重定向登录页），再按 editor 角色
// 判定卡组可写性——非 editor 仍回 403，不因切壳而把编辑壳交给无权用户。SPA 缺失（降级）时
// 回退 SSR 编辑页 noteEdit。
func (s *Server) noteEditRoute(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	if s.spa != nil {
		deckID, ok := deckIDParam(c)
		if !ok {
			return
		}
		if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleEditor); !ok {
			return
		}
		s.spa.ServeIndex(c)
		return
	}
	s.noteEdit(c)
}

// noteNewRoute 提供 GET /decks/:id/new-note：SPA 已加载时返回应用壳，由客户端路由渲染新建卡片页，
// 提交仍走 POST /api/v1/decks/:id/notes（DESIGN.md §8.1、§8.5）。
//
// 鉴权与迁移前的 SSR 新建页逐项一致：匿名重定向登录页，非 editor 回 403。SPA 缺失（降级）时
// 回退 SSR 新建页 noteNew。
func (s *Server) noteNewRoute(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	if s.spa != nil {
		deckID, ok := deckIDParam(c)
		if !ok {
			return
		}
		if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleEditor); !ok {
			return
		}
		s.spa.ServeIndex(c)
		return
	}
	s.noteNew(c)
}

// noteList 渲染卡片列表：分页、搜索、按标签与题型筛选（M2-7）。
func (s *Server) noteList(c *gin.Context) {
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
	deck, ok := s.loadDeckForRole(c, user, deckID, store.RoleReader)
	if !ok {
		return
	}

	q := strings.TrimSpace(c.Query("q"))
	tag := strings.TrimSpace(c.Query("tag"))
	kind := strings.TrimSpace(c.Query("kind"))
	status := normalizeStatus(c.Query("status"))
	page := parsePage(c.Query("page"))

	notes, total, err := s.notes.List(c.Request.Context(), store.NoteListOptions{
		DeckID:  deck.ID,
		Page:    page,
		PerPage: store.DefaultNotePageSize,
		Query:   q,
		Tag:     tag,
		Kind:    kind,
		Status:  status,
	})
	if err != nil {
		s.logger.Error("list notes failed", "deck_id", deck.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	rows := make([]views.NoteRow, 0, len(notes))
	for i := range notes {
		n := notes[i]
		fields, ferr := store.ParseFields(n.FieldsJSON)
		if ferr != nil {
			// 坏数据不应让整页 500：摘要留空，其余列照常展示。
			s.logger.Error("decode note fields failed", "note_id", n.ID, "error", ferr)
		}
		tags, terr := store.ParseTags(n.TagsJSON)
		if terr != nil {
			s.logger.Error("decode note tags failed", "note_id", n.ID, "error", terr)
		}
		statusLabel := loc.T("notes.list.status_active")
		if n.DeletedAt.Valid {
			statusLabel = loc.T("notes.list.status_deleted")
		}
		rows = append(rows, views.NoteRow{
			IDValue:     strconv.FormatUint(n.ID, 10),
			Kind:        n.Kind,
			KindLabel:   s.kindLabel(loc, n.Kind),
			Front:       noteSummary(fields),
			Tags:        tags,
			Created:     n.CreatedAt.UTC().Format("2006-01-02 15:04"),
			Deleted:     n.DeletedAt.Valid,
			StatusLabel: statusLabel,
			EditHref:    fmt.Sprintf("/decks/%d/notes/%d", deck.ID, n.ID),
		})
	}

	perPage := store.DefaultNotePageSize
	totalPages := int((total + int64(perPage) - 1) / int64(perPage))
	if totalPages < 1 {
		totalPages = 1
	}
	pagination := views.Pagination{
		PrevLabel: loc.T("notes.list.prev"),
		NextLabel: loc.T("notes.list.next"),
		PageLabel: loc.Tf("notes.list.page", map[string]any{"page": page, "pages": totalPages}),
	}
	if page > 1 {
		pagination.PrevHref = noteListHref(deck.ID, page-1, q, tag, kind, status)
	}
	if page < totalPages {
		pagination.NextHref = noteListHref(deck.ID, page+1, q, tag, kind, status)
	}

	data := views.NoteListData{
		Layout:             s.pageLayout(c, loc, "notes.list.title"),
		Heading:            loc.T("notes.list.heading"),
		DeckName:           deck.Name,
		FilterAction:       fmt.Sprintf("/decks/%d/notes", deck.ID),
		SearchLabel:        loc.T("notes.list.search_label"),
		SearchPlaceholder:  loc.T("notes.list.search_placeholder"),
		SearchValue:        q,
		TagLabel:           loc.T("notes.list.tag_label"),
		TagPlaceholder:     loc.T("notes.list.tag_placeholder"),
		TagValue:           tag,
		KindLabel:          loc.T("notes.list.kind_label"),
		KindValue:          kind,
		StatusLabel:        loc.T("notes.list.status_label"),
		StatusValue:        status,
		FilterSubmit:       loc.T("notes.list.filter_submit"),
		FilterReset:        loc.T("notes.list.filter_reset"),
		ResetHref:          fmt.Sprintf("/decks/%d/notes", deck.ID),
		Kinds:              s.kindOptions(loc, kind),
		Statuses:           s.statusOptions(loc, status),
		ColKind:            loc.T("notes.list.col_kind"),
		ColFront:           loc.T("notes.list.col_front"),
		ColTags:            loc.T("notes.list.col_tags"),
		ColCreated:         loc.T("notes.list.col_created"),
		ColStatus:          loc.T("notes.list.col_status"),
		Rows:               rows,
		EmptyText:          loc.T("notes.list.empty"),
		TotalLabel:         loc.Tf("notes.list.total", map[string]any{"count": total}),
		PageLabel:          pagination.PageLabel,
		Pagination:         pagination,
		BulkAction:         fmt.Sprintf("/decks/%d/bulk", deck.ID),
		BulkHint:           loc.T("notes.list.bulk_hint"),
		BulkDeleteLabel:    loc.T("notes.list.bulk_delete"),
		BulkTagLabel:       loc.T("notes.list.bulk_tag"),
		BulkTagPlaceholder: loc.T("notes.list.bulk_tag_placeholder"),
		BulkSubmitLabel:    loc.T("notes.list.bulk_submit"),
		NewNoteLabel:       loc.T("notes.list.new_note"),
		NewNoteHref:        fmt.Sprintf("/decks/%d/new-note", deck.ID),
		CloneAction:        fmt.Sprintf("/decks/%d/clone", deck.ID),
		CloneLabel:         loc.T("clone.submit"),
		Redirect:           noteListHref(deck.ID, page, q, tag, kind, status),
	}
	if sess, ok := auth.CurrentSession(c); ok {
		data.CSRF = sess.CSRFToken
	}
	renderHTMLStatus(c, http.StatusOK, views.NoteListPage(data))
}

// kindOptions 生成题型筛选下拉；首项是“全部”（值为空串）。
func (s *Server) kindOptions(loc *i18n.Localizer, current string) []views.KindOption {
	opts := []views.KindOption{{Value: "", Label: loc.T("notes.list.filter_all"), Selected: current == ""}}
	for _, kind := range cardtype.Kinds() {
		opts = append(opts, views.KindOption{
			Value:    kind,
			Label:    s.kindLabel(loc, kind),
			Selected: kind == current,
		})
	}
	return opts
}

// statusOptions 生成状态筛选下拉（全部 / 正常 / 已删除）。
func (s *Server) statusOptions(loc *i18n.Localizer, current string) []views.KindOption {
	return []views.KindOption{
		{Value: store.NoteStatusActive, Label: loc.T("notes.list.status_active"), Selected: current == store.NoteStatusActive},
		{Value: store.NoteStatusDeleted, Label: loc.T("notes.list.status_deleted"), Selected: current == store.NoteStatusDeleted},
		{Value: store.NoteStatusAll, Label: loc.T("notes.list.filter_all"), Selected: current == store.NoteStatusAll},
	}
}

// loadNoteInDeck 取 note 并确认它属于给定卡组；否则写 404。
func (s *Server) loadNoteInDeck(c *gin.Context, noteID, deckID uint64) (*store.Note, bool) {
	note, err := s.notes.ByID(c.Request.Context(), noteID)
	if err != nil || note.DeckID != deckID {
		c.AbortWithStatus(http.StatusNotFound)
		return nil, false
	}
	return note, true
}

// noteEdit 渲染编辑页；预览区用现有字段先渲染一次，之后由 htmx 局部刷新（M2-7）。
func (s *Server) noteEdit(c *gin.Context) {
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
	deck, ok := s.loadDeckForRole(c, user, deckID, store.RoleEditor)
	if !ok {
		return
	}
	noteID, err := strconv.ParseUint(c.Param("nid"), 10, 64)
	if err != nil || noteID == 0 {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	note, ok := s.loadNoteInDeck(c, noteID, deck.ID)
	if !ok {
		return
	}
	fields, perr := store.ParseFields(note.FieldsJSON)
	if perr != nil {
		s.logger.Error("decode note fields failed", "note_id", note.ID, "error", perr)
	}
	s.renderNoteEdit(c, loc, deck, note, fields, http.StatusOK, "")
}

// renderNoteEdit 渲染编辑页（含错误分支）。
func (s *Server) renderNoteEdit(c *gin.Context, loc *i18n.Localizer, deck *store.Deck, note *store.Note, fields map[string]any, status int, errMsg string) {
	data := views.NoteEditData{
		Layout:        s.pageLayout(c, loc, "notes.edit.title"),
		Heading:       loc.T("notes.edit.heading"),
		KindLabel:     s.kindLabel(loc, note.Kind),
		Fields:        s.fieldInputs(loc, fields),
		Action:        fmt.Sprintf("/decks/%d/notes/%d", deck.ID, note.ID),
		SaveLabel:     loc.T("notes.edit.save"),
		PreviewLabel:  loc.T("notes.edit.preview"),
		PreviewURL:    fmt.Sprintf("/decks/%d/preview", deck.ID),
		BackLabel:     loc.T("notes.edit.back"),
		BackHref:      fmt.Sprintf("/decks/%d/notes", deck.ID),
		NoteID:        strconv.FormatUint(note.ID, 10),
		ErrorMessage:  errMsg,
		Deleted:       note.DeletedAt.Valid,
		DeletedNotice: loc.T("notes.edit.deleted_notice"),
		Preview:       s.buildPreview(loc, note.Kind, fields),
		Upload:        s.mediaUploadData(c, loc, deck.ID),
		Picker:        s.mediaPickerData(loc, deck.ID),
	}
	if sess, ok := auth.CurrentSession(c); ok {
		data.CSRF = sess.CSRFToken
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(status)
	if err := views.NoteEditPage(data).Render(c.Request.Context(), c.Writer); err != nil {
		s.logger.Error("render template failed", "error", err, "path", c.Request.URL.Path)
	}
}

// fieldInputs 按固定顺序把字段转成可编辑输入框；字符串字段原样展示，复杂字段用紧凑 JSON。
func (s *Server) fieldInputs(loc *i18n.Localizer, fields map[string]any) []views.FieldInput {
	seen := make(map[string]bool)
	out := make([]views.FieldInput, 0, len(fields)+2)
	add := func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		v, ok := fields[name]
		if !ok {
			return
		}
		out = append(out, views.FieldInput{
			Name:  name,
			Label: s.fieldLabel(loc, name),
			Value: fieldToText(v),
		})
	}
	for _, name := range fieldOrder {
		add(name)
	}
	// 追加不在固定顺序里的字段，保证未来题型也能编辑（顺序按字典序，稳定可测）。
	extra := make([]string, 0, len(fields))
	for name := range fields {
		if !seen[name] {
			extra = append(extra, name)
		}
	}
	sortStrings(extra)
	for _, name := range extra {
		add(name)
	}
	return out
}

// fieldToText 把字段值转成文本：字符串原样，其余用紧凑 JSON（数组/对象/布尔/数字）。
func fieldToText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
}

// mergePostedFields 以 base 的类型为准，把表单里 field.* 的输入合并回字段映射。
// 这样字符串字段保持字符串、数组字段仍解析成数组，不会被误判成数字或布尔。
func mergePostedFields(c *gin.Context, base map[string]any) map[string]any {
	merged := make(map[string]any, len(base))
	for k, v := range base {
		merged[k] = v
	}
	if err := c.Request.ParseForm(); err != nil {
		return merged
	}
	for key, values := range c.Request.PostForm {
		if !strings.HasPrefix(key, "field.") || len(values) == 0 {
			continue
		}
		name := strings.TrimPrefix(key, "field.")
		raw := values[len(values)-1]
		switch base[name].(type) {
		case string:
			merged[name] = raw
		case bool:
			merged[name] = raw == "true"
		case float64:
			if f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil {
				merged[name] = f
			} else {
				merged[name] = raw
			}
		default:
			if strings.TrimSpace(raw) == "" {
				delete(merged, name)
				continue
			}
			var decoded any
			if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
				merged[name] = decoded
			} else {
				merged[name] = raw
			}
		}
	}
	return merged
}

// buildPreview 用 internal/render 渲染每个 card 的正反面，供编辑页预览与 htmx 片段使用。
func (s *Server) buildPreview(loc *i18n.Localizer, kind string, fields map[string]any) views.NotePreviewData {
	data := views.NotePreviewData{
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
		data.Cards = append(data.Cards, views.PreviewCard{
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

// notePreview 是 htmx 局部刷新端点：读取表单字段、渲染并返回预览片段（不含整页外壳）。
func (s *Server) notePreview(c *gin.Context) {
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
	deck, ok := s.loadDeckForRole(c, user, deckID, store.RoleEditor)
	if !ok {
		return
	}
	noteID, err := strconv.ParseUint(strings.TrimSpace(c.PostForm("note_id")), 10, 64)
	if err != nil || noteID == 0 {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	note, ok := s.loadNoteInDeck(c, noteID, deck.ID)
	if !ok {
		return
	}
	fields, perr := store.ParseFields(note.FieldsJSON)
	if perr != nil {
		s.logger.Error("decode note fields failed", "note_id", note.ID, "error", perr)
	}
	merged := mergePostedFields(c, fields)
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(http.StatusOK)
	if err := views.NotePreviewFragment(s.buildPreview(loc, note.Kind, merged)).Render(c.Request.Context(), c.Writer); err != nil {
		s.logger.Error("render preview fragment failed", "error", err)
	}
}

// noteUpdate 保存编辑表单：走 NoteStore.Update（校验 + 同步 cards），失败时回显错误。
func (s *Server) noteUpdate(c *gin.Context) {
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
	deck, ok := s.loadDeckForRole(c, user, deckID, store.RoleEditor)
	if !ok {
		return
	}
	noteID, err := strconv.ParseUint(c.Param("nid"), 10, 64)
	if err != nil || noteID == 0 {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	note, ok := s.loadNoteInDeck(c, noteID, deck.ID)
	if !ok {
		return
	}
	fields, perr := store.ParseFields(note.FieldsJSON)
	if perr != nil {
		s.logger.Error("decode note fields failed", "note_id", note.ID, "error", perr)
	}
	merged := mergePostedFields(c, fields)

	// 写前校验的执行者：据此判断本次新引入的引用是否编辑者可读（DESIGN.md §6.3）。
	ctx := store.WithActor(c.Request.Context(), user.ID)
	if _, err := s.notes.Update(ctx, note, merged); err != nil {
		s.logger.Info("update note rejected", "note_id", note.ID, "error", err)
		msg := loc.T("notes.edit.invalid_fields") + err.Error()
		if isMediaNotReadable(err) {
			msg = loc.T("notes.edit.media_not_readable")
		}
		s.renderNoteEdit(c, loc, deck, note, merged, http.StatusBadRequest, msg)
		return
	}
	s.audit(c.Request.Context(), store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionNoteUpdate,
		TargetType: "note",
		TargetID:   store.Ptr(note.ID),
		Detail:     map[string]any{"deck_id": deck.ID},
	})
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/decks/%d/notes/%d", deck.ID, note.ID))
}

// noteBulk 执行批量操作：软删除或加标签（M2-7）。
//
// 只接受属于当前卡组的 note id，避免把其它卡组/用户的卡片一并改动（越权防线）。
// 每个批量动作写一行审计（M1-10 的统一出口）。
//
// 这里调 store 的批量原语（DeleteMany/AddTags），而不是 api.BulkNotes：网页是「针对当前卡组」
// 的批量表单 —— 先 loadDeckForRole 判权、再用 allowedNoteIDs 把 id 收窄到本卡组、审计记
// target_type=deck；service 层那套则是「按 note 逐行判权 + 行级 skipped + 整批一行
// target_type=notes」（POST /notes/bulk）。两者的语义与取证口径不同，不是同一段逻辑的两份实现；
// 真正共用的部分（四个 store 原语）本来就是同一份代码。**不要只把这一处改成走 service**：
// internal/web 整体是 store 层视图（decks.go/review.go/clone.go 等 20 余处同理），单独改这一处
// 会让网页层出现唯一的例外，并顺手改掉它的审计形态。
func (s *Server) noteBulk(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	deckID, ok := deckIDParam(c)
	if !ok {
		return
	}
	deck, ok := s.loadDeckForRole(c, user, deckID, store.RoleEditor)
	if !ok {
		return
	}
	action := strings.TrimSpace(c.PostForm("action"))
	ids := s.allowedNoteIDs(c.Request.Context(), deck.ID, c.PostFormArray("note_ids"))
	back := safeRedirect(c.PostForm("redirect"), deck.ID)
	if len(ids) == 0 {
		c.Redirect(http.StatusSeeOther, back)
		return
	}

	switch action {
	case "delete":
		n, err := s.notes.DeleteMany(c.Request.Context(), ids, false)
		if err != nil {
			s.logger.Error("bulk delete notes failed", "deck_id", deck.ID, "error", err)
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		s.audit(c.Request.Context(), store.AuditEntry{
			UserID:     store.Ptr(user.ID),
			Action:     store.ActionNoteDelete,
			TargetType: "deck",
			TargetID:   store.Ptr(deck.ID),
			Detail:     map[string]any{"ids": ids, "deleted": n},
		})
	case "tag":
		tag := strings.TrimSpace(c.PostForm("tag"))
		n, err := s.notes.AddTags(c.Request.Context(), ids, []string{tag}, false)
		if err != nil {
			// 空标签是用户输入问题：回列表并记日志，不 500。
			s.logger.Info("bulk add tag rejected", "deck_id", deck.ID, "error", err)
			c.Redirect(http.StatusSeeOther, back)
			return
		}
		s.audit(c.Request.Context(), store.AuditEntry{
			UserID:     store.Ptr(user.ID),
			Action:     store.ActionNoteTagAdd,
			TargetType: "deck",
			TargetID:   store.Ptr(deck.ID),
			Detail:     map[string]any{"ids": ids, "tag": tag, "updated": n},
		})
	default:
		// 未知动作直接回列表；不写审计，因为没有发生变更。
	}
	c.Redirect(http.StatusSeeOther, back)
}

// allowedNoteIDs 过滤出确实属于该卡组的 note id（软删除的 note 由 ByID 视为不存在）。
func (s *Server) allowedNoteIDs(ctx context.Context, deckID uint64, raw []string) []uint64 {
	out := make([]uint64, 0, len(raw))
	for _, item := range raw {
		id, err := strconv.ParseUint(strings.TrimSpace(item), 10, 64)
		if err != nil || id == 0 {
			continue
		}
		note, err := s.notes.ByID(ctx, id)
		if err != nil || note.DeckID != deckID {
			continue
		}
		out = append(out, id)
	}
	return out
}

// safeRedirect 把批量操作的回跳地址限制在本卡组的列表页，防止开放重定向。
func safeRedirect(raw string, deckID uint64) string {
	fallback := fmt.Sprintf("/decks/%d/notes", deckID)
	if !strings.HasPrefix(raw, "/decks/") || strings.Contains(raw, "://") || strings.HasPrefix(raw, "//") {
		return fallback
	}
	return raw
}

// sortStrings 是小规模切片的插入排序，避免为一个辅助函数引入 sort 依赖差异。
func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

// resolveCreateKind 把 UI 传来的 kind 收窄到已注册题型；非法或缺失时退回第一个内置题型。
// 未知 kind 不在这里报错：表单页需要一个可用题型，真正的拒绝由 cardtype 在写入时报出。
func resolveCreateKind(raw string) string {
	kind := strings.TrimSpace(raw)
	if _, ok := cardtype.Lookup(kind); ok {
		return kind
	}
	if kinds := cardtype.Kinds(); len(kinds) > 0 {
		return kinds[0]
	}
	return "basic"
}

// createKindOptions 生成新建表单的题型下拉（只列真实题型，不含筛选页的“全部”项）。
func (s *Server) createKindOptions(loc *i18n.Localizer, current string) []views.KindOption {
	kinds := cardtype.Kinds()
	opts := make([]views.KindOption, 0, len(kinds))
	for _, kind := range kinds {
		opts = append(opts, views.KindOption{
			Value:    kind,
			Label:    s.kindLabel(loc, kind),
			Selected: kind == current,
		})
	}
	return opts
}

// emptyPreview 返回只有区域标签、没有卡片的预览数据；新建页初始状态用它，
// 避免一进页面就显示"缺字段"错误（那时用户还没输入任何内容）。
func emptyPreview(loc *i18n.Localizer) views.NotePreviewData {
	return views.NotePreviewData{
		Title:      loc.T("notes.preview.title"),
		FrontLabel: loc.T("notes.preview.front"),
		BackLabel:  loc.T("notes.preview.back"),
	}
}

// noteNew 渲染新建卡片页（M2-12）；匿名跳登录、非 owner 403。
func (s *Server) noteNew(c *gin.Context) {
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
	deck, ok := s.loadDeckForRole(c, user, deckID, store.RoleEditor)
	if !ok {
		return
	}
	s.renderNoteNew(c, loc, deck, resolveCreateKind(c.Query("kind")), nil, nil, http.StatusOK, "")
}

// renderNoteNew 渲染新建卡片页；raw 是失败时回显的原始表单值，fields 是已转换的字段（nil 表示初始状态）。
func (s *Server) renderNoteNew(c *gin.Context, loc *i18n.Localizer, deck *store.Deck, kind string, raw map[string]string, fields map[string]any, status int, errMsg string) {
	preview := emptyPreview(loc)
	if fields != nil {
		preview = s.buildPreview(loc, kind, fields)
	}
	data := views.NewNoteData{
		Layout:       s.pageLayout(c, loc, "notes.create.title"),
		Heading:      loc.T("notes.create.heading"),
		KindLabel:    loc.T("notes.create.kind_label"),
		Kinds:        s.createKindOptions(loc, kind),
		Fields:       s.createFields(loc, kind, raw),
		FieldsURL:    fmt.Sprintf("/decks/%d/new-note/fields", deck.ID),
		PreviewURL:   fmt.Sprintf("/decks/%d/preview-new", deck.ID),
		Action:       fmt.Sprintf("/decks/%d/notes", deck.ID),
		SaveLabel:    loc.T("notes.create.save"),
		BackLabel:    loc.T("notes.edit.back"),
		BackHref:     fmt.Sprintf("/decks/%d/notes", deck.ID),
		ErrorMessage: errMsg,
		Preview:      preview,
		Upload:       s.mediaUploadData(c, loc, deck.ID),
		Picker:       s.mediaPickerData(loc, deck.ID),
	}
	if sess, ok := auth.CurrentSession(c); ok {
		data.CSRF = sess.CSRFToken
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(status)
	if err := views.NoteCreatePage(data).Render(c.Request.Context(), c.Writer); err != nil {
		s.logger.Error("render template failed", "error", err, "path", c.Request.URL.Path)
	}
}

// noteFieldsFragment 是 htmx 片段：按 ?kind= 返回该题型的字段输入区（M2-12 题型切换）。
func (s *Server) noteFieldsFragment(c *gin.Context) {
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
	if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleEditor); !ok {
		return
	}
	kind := resolveCreateKind(c.Query("kind"))
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(http.StatusOK)
	if err := views.NoteFieldsFragment(s.createFields(loc, kind, nil), fmt.Sprintf("/decks/%d/preview-new", deckID)).
		Render(c.Request.Context(), c.Writer); err != nil {
		s.logger.Error("render fields fragment failed", "error", err)
	}
}

// noteCreatePreview 是 htmx 片段：对尚未保存的新卡片渲染预览（不写库）。
func (s *Server) noteCreatePreview(c *gin.Context) {
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
	if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleEditor); !ok {
		return
	}
	kind := resolveCreateKind(c.PostForm("kind"))
	fields := collectCreateFields(c, kind)
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(http.StatusOK)
	if err := views.NotePreviewFragment(s.buildPreview(loc, kind, fields)).Render(c.Request.Context(), c.Writer); err != nil {
		s.logger.Error("render create preview fragment failed", "error", err)
	}
}

// noteCreate 处理新建卡片表单：转换字段 → NoteStore.Create（题型校验 + 生成 cards）→ 写审计。
// 校验失败时回显 400 与本地化错误，成功则回到卡片列表（新卡片已可见）。
func (s *Server) noteCreate(c *gin.Context) {
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
	deck, ok := s.loadDeckForRole(c, user, deckID, store.RoleEditor)
	if !ok {
		return
	}
	kind := resolveCreateKind(c.PostForm("kind"))
	raw := rawCreateValues(c, kind)
	fields := collectCreateFields(c, kind)

	source := "manual"
	note := &store.Note{
		DeckID:    deck.ID,
		Kind:      kind,
		Source:    &source,
		CreatedBy: store.Ptr(user.ID),
	}
	// 写前校验的执行者：据此判断本次新引入的引用是否作者可读（DESIGN.md §6.3）。
	ctx := store.WithActor(c.Request.Context(), user.ID)
	cards, err := s.notes.Create(ctx, note, fields)
	if err != nil {
		s.logger.Info("create note rejected", "deck_id", deck.ID, "kind", kind, "error", err)
		msg := loc.T("notes.create.error_invalid") + err.Error()
		if isMediaNotReadable(err) {
			msg = loc.T("notes.edit.media_not_readable")
		}
		s.renderNoteNew(c, loc, deck, kind, raw, fields, http.StatusBadRequest, msg)
		return
	}
	s.audit(c.Request.Context(), store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionNoteCreate,
		TargetType: "note",
		TargetID:   store.Ptr(note.ID),
		Detail:     map[string]any{"deck_id": deck.ID, "kind": kind, "cards": len(cards)},
	})
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/decks/%d/notes", deck.ID))
}
