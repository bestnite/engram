package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// importResponse 保留旧名，避免既有调用处改名（真实定义在 service.go，REST 与 MCP 共用）。
type importResponse = ImportResponse

// listNotes 返回卡组下的卡片列表（分页、标签过滤、关键词搜索）。
func (a *API) listNotes(c *gin.Context) {
	u, _ := CurrentUser(c)
	deckID, ok := pathID(c, "id")
	if !ok {
		return
	}
	opts := store.NormalizeNoteListOptions(store.NoteListOptions{
		Page:    queryInt(c, "page", 1),
		PerPage: queryInt(c, "per_page", store.DefaultNotePageSize),
		Query:   c.Query("q"),
		Tag:     c.Query("tag"),
		Kind:    c.Query("kind"),
		Status:  c.Query("status"),
	})
	notes, total, err := a.ListNotes(c.Request.Context(), u.ID, deckID, opts)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	out := make([]map[string]any, 0, len(notes))
	for i := range notes {
		out = append(out, NoteJSON(&notes[i]))
	}
	// 回显的生效值就是 NormalizeNoteListOptions 的结果，不再在本处重复夹一次。
	c.JSON(http.StatusOK, gin.H{"notes": out, "total": total, "page": opts.Page, "per_page": opts.PerPage})
}

// NoteJSON 把 note 转成对外形态：fields 与 tags 解码成结构化值，避免调用方二次解析。
// REST（list_notes/update_note）与 MCP 同名工具共用，保证同一 note 产出同一 JSON。
func NoteJSON(n *store.Note) map[string]any {
	fields := store.FieldsOrEmpty(n.FieldsJSON)
	tags := store.TagsOrEmpty(n.TagsJSON)
	return map[string]any{
		"id":           n.ID,
		"deck_id":      n.DeckID,
		"kind":         n.Kind,
		"fields":       fields,
		"tags":         tags,
		"created_at":   n.CreatedAt,
		"updated_at":   n.UpdatedAt,
		"external_ref": n.ExternalRef,
	}
}

// importNotes 批量新增/更新卡片：按 (deck_id, external_ref) 幂等。
func (a *API) importNotes(c *gin.Context) {
	u, _ := CurrentUser(c)
	deckID, ok := pathID(c, "id")
	if !ok {
		return
	}
	ctx := c.Request.Context()
	var req ImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	resp, err := a.ImportNotes(ctx, u.ID, deckID, CurrentAPIKeyID(c), req)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
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
	var req updateNoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	updated, err := a.UpdateNote(c.Request.Context(), u.ID, noteID, CurrentAPIKeyID(c), UpdateNoteInput{
		Kind:   req.Kind,
		Fields: req.Fields,
		Tags:   req.Tags,
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, NoteJSON(updated))
}

// deleteNote 软删除单卡（进度保留，误删可恢复）。
func (a *API) deleteNote(c *gin.Context) {
	u, _ := CurrentUser(c)
	noteID, ok := pathID(c, "id")
	if !ok {
		return
	}
	deleted, err := a.DeleteNote(c.Request.Context(), u.ID, noteID, CurrentAPIKeyID(c))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true, "id": deleted})
}

// bulkNotes 对一组 note 执行 delete / add_tags / remove_tags / set_tags（M4-12）。
// 请求体形态与 BulkNotes 输入一致；错误走统一包壳，成功 200 返回 BulkNotesResponse。
func (a *API) bulkNotes(c *gin.Context) {
	u, _ := CurrentUser(c)
	var req BulkNotesInput
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	resp, err := a.BulkNotes(c.Request.Context(), u.ID, CurrentAPIKeyID(c), req)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}
