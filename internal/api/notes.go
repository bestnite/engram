package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"example.com/flashcard/internal/cardtype"
	"example.com/flashcard/internal/store"
)

// importNote 是批量导入的单个 note；字段名与 schema/note-import.schema.json 一致（DESIGN.md §7.3）。
type importNote struct {
	Kind        string         `json:"kind"`
	Fields      map[string]any `json:"fields"`
	ExternalRef string         `json:"external_ref"`
	Tags        []string       `json:"tags"`
}

// importRequest 是 POST /decks/:id/notes 的请求体。
// dry_run 与 on_conflict 是请求体字段（不是查询串），单次最多 500 条。
type importRequest struct {
	DryRun     bool         `json:"dry_run"`
	OnConflict string       `json:"on_conflict"`
	Notes      []importNote `json:"notes"`
}

// importError 是逐条导入错误：index 指向请求体 notes 数组的下标。
type importError struct {
	Index  int    `json:"index"`
	Reason string `json:"reason"`
}

// importResponse 是批量导入的响应体（DESIGN.md §7.3）。
type importResponse struct {
	DryRun  bool          `json:"dry_run"`
	Created int           `json:"created"`
	Updated int           `json:"updated"`
	Skipped int           `json:"skipped"`
	Errors  []importError `json:"errors"`
}

const maxImportNotes = 500

// listNotes 返回卡组下的卡片列表（分页、标签过滤、关键词搜索）。
func (a *API) listNotes(c *gin.Context) {
	deckID, ok := pathID(c, "id")
	if !ok {
		return
	}
	d, ok := a.ownedDeck(c, deckID)
	if !ok {
		return
	}
	page := queryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	perPage := queryInt(c, "per_page", store.DefaultNotePageSize)
	opts := store.NoteListOptions{
		DeckID:  d.ID,
		Page:    page,
		PerPage: perPage,
		Query:   c.Query("q"),
		Tag:     c.Query("tag"),
		Kind:    c.Query("kind"),
		Status:  c.Query("status"),
	}
	notes, total, err := a.notes.List(c.Request.Context(), opts)
	if err != nil {
		a.logger.Error("list notes failed", "deck_id", d.ID, "error", err)
		abortError(c, http.StatusInternalServerError, CodeInternal, "failed to list notes")
		return
	}
	out := make([]gin.H, 0, len(notes))
	for i := range notes {
		out = append(out, noteJSON(&notes[i]))
	}
	c.JSON(http.StatusOK, gin.H{"notes": out, "total": total, "page": opts.Page, "per_page": opts.PerPage})
}

// noteJSON 把 note 转成对外形态：fields 与 tags 解码成结构化值，避免调用方二次解析。
func noteJSON(n *store.Note) gin.H {
	fields, _ := store.ParseFields(n.FieldsJSON)
	if fields == nil {
		fields = map[string]any{}
	}
	tags, _ := store.ParseTags(n.TagsJSON)
	if tags == nil {
		tags = []string{}
	}
	item := gin.H{
		"id":           n.ID,
		"deck_id":      n.DeckID,
		"kind":         n.Kind,
		"fields":       fields,
		"tags":         tags,
		"created_at":   n.CreatedAt,
		"updated_at":   n.UpdatedAt,
		"external_ref": n.ExternalRef,
	}
	return item
}

// importNotes 批量新增/更新卡片：按 (deck_id, external_ref) 幂等（DESIGN.md §7.3）。
//
// 采用两遍法：第一遍校验并规划每条的去向（create/update/skip），第二遍才写库。
// dry_run=1 时停在第一遍，只返回计数；on_conflict=fail 时任何冲突或校验错误都会整批拒绝
// （不写任何行），update（默认）与 skip 则逐条处理并在 errors 里回报失败行。
func (a *API) importNotes(c *gin.Context) {
	u, _ := CurrentUser(c)
	deckID, ok := pathID(c, "id")
	if !ok {
		return
	}
	d, ok := a.ownedDeck(c, deckID)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	var req importRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "request body must be valid JSON")
		return
	}
	if len(req.Notes) == 0 || len(req.Notes) > maxImportNotes {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest,
			fmt.Sprintf("notes must contain between 1 and %d items", maxImportNotes))
		return
	}
	onConflict := strings.TrimSpace(req.OnConflict)
	if onConflict == "" {
		onConflict = "update"
	}
	if onConflict != "skip" && onConflict != "update" && onConflict != "fail" {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "on_conflict must be one of skip, update, fail")
		return
	}

	type plan struct {
		index    int
		action   string
		existing *store.Note
		note     store.Note
		fields   map[string]any
	}
	plans := make([]plan, 0, len(req.Notes))
	resp := importResponse{DryRun: req.DryRun, Errors: []importError{}}
	hasFailure := false

	for i, item := range req.Notes {
		item.Kind = strings.TrimSpace(item.Kind)
		if item.Kind == "" {
			resp.Errors = append(resp.Errors, importError{i, "kind is required"})
			hasFailure = true
			continue
		}
		if len(item.Fields) == 0 {
			resp.Errors = append(resp.Errors, importError{i, "fields are required"})
			hasFailure = true
			continue
		}
		if err := cardtype.Validate(item.Kind, item.Fields); err != nil {
			resp.Errors = append(resp.Errors, importError{i, err.Error()})
			hasFailure = true
			continue
		}

		ref := strings.TrimSpace(item.ExternalRef)
		var existing *store.Note
		if ref != "" {
			found, err := a.findNoteByExternalRef(ctx, d.ID, ref)
			if err != nil {
				a.logger.Error("lookup note by external_ref failed", "deck_id", d.ID, "error", err)
				abortError(c, http.StatusInternalServerError, CodeInternal, "failed to load existing notes")
				return
			}
			existing = found
		}

		n := store.Note{
			DeckID:    d.ID,
			Kind:      item.Kind,
			TagsJSON:  tagsJSON(item.Tags),
			CreatedBy: store.Ptr(u.ID),
			Source:    store.Ptr("api"),
		}
		if ref != "" {
			n.ExternalRef = store.Ptr(ref)
		}

		action := "create"
		if existing != nil {
			switch onConflict {
			case "skip":
				action = "skip"
			case "fail":
				resp.Errors = append(resp.Errors, importError{i, "a note with this external_ref already exists"})
				hasFailure = true
				continue
			default:
				action = "update"
			}
		}
		plans = append(plans, plan{index: i, action: action, existing: existing, note: n, fields: item.Fields})
	}

	if onConflict == "fail" && hasFailure {
		abortError(c, http.StatusConflict, CodeConflict, "import aborted: one or more notes are invalid or conflict")
		return
	}

	if req.DryRun {
		for _, p := range plans {
			countPlan(&resp, p.action)
		}
		c.JSON(http.StatusOK, resp)
		return
	}

	for _, p := range plans {
		if p.action == "skip" {
			resp.Skipped++
			continue
		}
		if p.action == "create" {
			n := p.note
			if _, err := a.notes.Create(ctx, &n, p.fields); err != nil {
				resp.Errors = append(resp.Errors, importError{p.index, err.Error()})
				continue
			}
			resp.Created++
			continue
		}
		// update：已软删除的 note 先恢复再更新，否则 Update 会把它当不存在。
		if p.existing.DeletedAt.Valid {
			if err := a.notes.Restore(ctx, p.existing.ID); err != nil {
				resp.Errors = append(resp.Errors, importError{p.index, err.Error()})
				continue
			}
		}
		n := p.note
		n.ID = p.existing.ID
		if _, err := a.notes.Update(ctx, &n, p.fields); err != nil {
			resp.Errors = append(resp.Errors, importError{p.index, err.Error()})
			continue
		}
		resp.Updated++
	}

	a.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		APIKeyID:   CurrentAPIKeyID(c),
		Action:     "note.import",
		TargetType: "deck",
		TargetID:   store.Ptr(d.ID),
		Detail: map[string]any{
			"created": resp.Created, "updated": resp.Updated,
			"skipped": resp.Skipped, "errors": len(resp.Errors), "dry_run": req.DryRun,
		},
	})
	c.JSON(http.StatusOK, resp)
}

// countPlan 把一条规划计入 dry_run 的计数。
func countPlan(resp *importResponse, action string) {
	switch action {
	case "create":
		resp.Created++
	case "update":
		resp.Updated++
	case "skip":
		resp.Skipped++
	}
}

// tagsJSON 把标签编码进 notes.tags_json；nil 也落成 "[]"，避免列里出现空串。
func tagsJSON(tags []string) string {
	if tags == nil {
		tags = []string{}
	}
	raw, err := json.Marshal(tags)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// findNoteByExternalRef 按 (deck_id, external_ref) 查 note，包含已软删除的行
// —— 唯一索引对软删除行同样生效，必须 Unscoped 查询，否则重复导入会撞唯一约束。
func (a *API) findNoteByExternalRef(ctx context.Context, deckID uint64, ref string) (*store.Note, error) {
	var n store.Note
	err := a.db.WithContext(ctx).Unscoped().
		Where("deck_id = ? AND external_ref = ?", deckID, ref).First(&n).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// updateNoteRequest 是单卡更新请求体；tags 用指针区分“未提供”与“清空”。
type updateNoteRequest struct {
	Kind   string         `json:"kind"`
	Fields map[string]any `json:"fields"`
	Tags   *[]string      `json:"tags"`
}

// updateNote 更新单卡内容；已有 card 的 id 与用户进度保持不变（NoteStore.Update 的保证）。
func (a *API) updateNote(c *gin.Context) {
	u, _ := CurrentUser(c)
	noteID, ok := pathID(c, "id")
	if !ok {
		return
	}
	existing, _, ok := a.ownedNote(c, noteID)
	if !ok {
		return
	}
	var req updateNoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "request body must be valid JSON")
		return
	}
	if len(req.Fields) == 0 {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "fields are required")
		return
	}
	kind := strings.TrimSpace(req.Kind)
	if kind == "" {
		kind = existing.Kind
	}
	// TODO(M4-9): error message localisation via Accept-Language.
	if err := cardtype.Validate(kind, req.Fields); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	n := store.Note{ID: existing.ID, Kind: kind}
	if req.Tags != nil {
		n.TagsJSON = tagsJSON(*req.Tags)
	} else {
		n.TagsJSON = existing.TagsJSON
	}
	if _, err := a.notes.Update(c.Request.Context(), &n, req.Fields); err != nil {
		a.logger.Error("update note failed", "note_id", existing.ID, "error", err)
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	a.audit(c.Request.Context(), store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		APIKeyID:   CurrentAPIKeyID(c),
		Action:     store.ActionNoteUpdate,
		TargetType: "note",
		TargetID:   store.Ptr(existing.ID),
	})
	updated, err := a.notes.ByID(c.Request.Context(), existing.ID)
	if err != nil {
		abortError(c, http.StatusInternalServerError, CodeInternal, "failed to reload note")
		return
	}
	c.JSON(http.StatusOK, noteJSON(updated))
}

// deleteNote 软删除单卡（进度保留，误删可恢复）。
func (a *API) deleteNote(c *gin.Context) {
	u, _ := CurrentUser(c)
	noteID, ok := pathID(c, "id")
	if !ok {
		return
	}
	existing, _, ok := a.ownedNote(c, noteID)
	if !ok {
		return
	}
	if err := a.notes.Delete(c.Request.Context(), existing.ID); err != nil {
		a.logger.Error("delete note failed", "note_id", existing.ID, "error", err)
		abortError(c, http.StatusInternalServerError, CodeInternal, "failed to delete note")
		return
	}
	a.audit(c.Request.Context(), store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		APIKeyID:   CurrentAPIKeyID(c),
		Action:     store.ActionNoteDelete,
		TargetType: "note",
		TargetID:   store.Ptr(existing.ID),
	})
	c.JSON(http.StatusOK, gin.H{"deleted": true, "id": existing.ID})
}
