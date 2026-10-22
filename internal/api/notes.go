package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// importResponse keeps the old name so existing call sites need no rename (the real definition is in service.go, shared by REST and MCP).
type importResponse = ImportResponse

// listNotes returns the card list under a deck (paging, tag filtering, keyword search).
func (a *API) listNotes(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	publicID, ok := pathPublicID(c, "id")
	if !ok {
		return
	}
	d, err := a.decks.ByPublicID(ctx, publicID)
	if err != nil {
		abortNotFound(c)
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
	notes, total, err := a.ListNotes(ctx, u.ID, d.ID, opts)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	out := a.NotesJSON(ctx, u.ID, notes)
	// The effective values echoed back are exactly the result of NormalizeNoteListOptions, not clamped a second time here.
	c.JSON(http.StatusOK, gin.H{"notes": out, "total": total, "page": opts.Page, "per_page": opts.PerPage})
}

// listDeckTags lists the tags inside a deck with their note counts, scope: read (the logic lives in the service layer's DeckTags).
// Used to pick tags when reviewing by tag.
func (a *API) listDeckTags(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	publicID, ok := pathPublicID(c, "id")
	if !ok {
		return
	}
	d, err := a.decks.ByPublicID(ctx, publicID)
	if err != nil {
		abortNotFound(c)
		return
	}
	tags, err := a.DeckTags(ctx, u.ID, d.ID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"tags": tags})
}

// NotesJSON turns a set of notes into the external shape as seen by userID: fields and tags are decoded into structured values,
// sparing callers a second parse. Shared by REST (list_notes/update_note) and the MCP tools of the same names, so one note yields one JSON.
//
// id is the note's own external id; deck_id is the external id of the deck it belongs to - numeric primary keys are never exposed.
// suspended means the caller suspended the cards under this note (suspension is personal), fetched for a whole page of notes in one query.
func (a *API) NotesJSON(ctx context.Context, userID uint64, notes []store.Note) []map[string]any {
	ids := make([]uint64, 0, len(notes))
	for i := range notes {
		ids = append(ids, notes[i].ID)
	}
	suspended, err := store.SuspendedNoteIDs(ctx, a.db, userID, ids)
	if err != nil {
		a.logger.Error("load suspended notes failed", "user_id", userID, "error", err)
		suspended = map[uint64]bool{}
	}
	deckPublic := map[uint64]string{}
	out := make([]map[string]any, 0, len(notes))
	for i := range notes {
		n := &notes[i]
		pid, ok := deckPublic[n.DeckID]
		if !ok {
			if d, err := a.decks.ByID(ctx, n.DeckID); err == nil {
				pid = d.PublicID
			}
			deckPublic[n.DeckID] = pid
		}
		out = append(out, map[string]any{
			"id":           n.PublicID,
			"deck_id":      pid,
			"kind":         n.Kind,
			"fields":       store.FieldsOrEmpty(n.FieldsJSON),
			"tags":         store.TagsOrEmpty(n.TagsJSON),
			"created_at":   n.CreatedAt,
			"updated_at":   n.UpdatedAt,
			"external_ref": n.ExternalRef,
			"suspended":    suspended[n.ID],
		})
	}
	return out
}

// getNote reads a single note (GET /api/v1/notes/:id, scope: read).
// The edit page fetches the target row directly with it instead of paging through the list to "find" it.
func (a *API) getNote(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	publicID, ok := pathPublicID(c, "id")
	if !ok {
		return
	}
	n, err := a.GetNote(ctx, u.ID, publicID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, a.NotesJSON(ctx, u.ID, []store.Note{*n})[0])
}

// importNotes bulk-adds/updates cards: idempotent per (deck_id, external_ref).
func (a *API) importNotes(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	publicID, ok := pathPublicID(c, "id")
	if !ok {
		return
	}
	d, err := a.decks.ByPublicID(ctx, publicID)
	if err != nil {
		abortNotFound(c)
		return
	}
	var req ImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	resp, err := a.ImportNotes(ctx, u.ID, d.ID, CurrentAPIKeyID(c), req)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// updateNoteRequest is the single-card update body; tags uses a pointer to distinguish "not provided" from "clear it".
type updateNoteRequest struct {
	Kind   string         `json:"kind"`
	Fields map[string]any `json:"fields"`
	Tags   *[]string      `json:"tags"`
}

// updateNote updates a card's content; the id and user progress of existing cards stay unchanged (guaranteed by NoteStore.Update).
func (a *API) updateNote(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	publicID, ok := pathPublicID(c, "id")
	if !ok {
		return
	}
	n, err := a.notes.ByPublicID(ctx, publicID)
	if err != nil {
		abortNotFound(c)
		return
	}
	var req updateNoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	updated, err := a.UpdateNote(ctx, u.ID, n.ID, CurrentAPIKeyID(c), UpdateNoteInput{
		Kind:   req.Kind,
		Fields: req.Fields,
		Tags:   req.Tags,
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, a.NotesJSON(ctx, u.ID, []store.Note{*updated})[0])
}

// deleteNote soft-deletes a card (progress is kept, an accidental delete can be undone).
func (a *API) deleteNote(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	publicID, ok := pathPublicID(c, "id")
	if !ok {
		return
	}
	n, err := a.notes.ByPublicID(ctx, publicID)
	if err != nil {
		abortNotFound(c)
		return
	}
	if _, err := a.DeleteNote(ctx, u.ID, n.ID, CurrentAPIKeyID(c)); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true, "id": publicID})
}

// bulkNotes performs delete / add_tags / remove_tags / set_tags on a set of notes.
// The body shape matches BulkNotes' input; errors go through the unified envelope, success returns 200 with BulkNotesResponse.
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
