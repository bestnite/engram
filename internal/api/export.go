package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"example.com/flashcard/internal/store"
)

// exportRow 是卡片级导出的扁平行（DESIGN.md §7.5：给外部工具用的粒度）。
type exportRow struct {
	CardID      uint64         `json:"card_id"`
	NoteID      uint64         `json:"note_id"`
	DeckID      uint64         `json:"deck_id"`
	Kind        string         `json:"kind"`
	Template    string         `json:"template"`
	Fields      map[string]any `json:"fields"`
	Tags        []string       `json:"tags"`
	ExternalRef *string        `json:"external_ref,omitempty"`
	State       *string        `json:"state,omitempty"`
	DueAt       *time.Time     `json:"due_at,omitempty"`
	Reps        *int           `json:"reps,omitempty"`
	Lapses      *int           `json:"lapses,omitempty"`
}

// exportCards 以 json 或 csv 导出卡片级内容；include_progress=1 时附带调用者本人的进度。
// 导出他人共享的卡组时绝不包含他人进度（DESIGN.md §7.6 的同一条边界）。
func (a *API) exportCards(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()

	decks, err := a.decks.ListByOwner(ctx, u.ID)
	if err != nil {
		abortError(c, http.StatusInternalServerError, CodeInternal, "failed to load decks")
		return
	}
	deckIDs := make([]uint64, 0, len(decks))
	for _, d := range decks {
		deckIDs = append(deckIDs, d.ID)
	}
	if raw := c.Query("deck"); raw != "" {
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || id == 0 {
			abortError(c, http.StatusBadRequest, CodeInvalidRequest, "deck must be a positive integer")
			return
		}
		if _, ok := a.ownedDeck(c, id); !ok {
			return
		}
		deckIDs = []uint64{id}
	}
	format := c.DefaultQuery("format", "json")
	if format != "json" && format != "csv" {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "format must be json or csv")
		return
	}
	includeProgress := c.Query("include_progress") == "1"

	rows, err := a.exportRows(ctx, u.ID, deckIDs, includeProgress)
	if err != nil {
		a.logger.Error("export query failed", "user_id", u.ID, "error", err)
		abortError(c, http.StatusInternalServerError, CodeInternal, "failed to export cards")
		return
	}

	if format == "csv" {
		a.writeCSV(c, rows, includeProgress)
		return
	}
	out := make([]exportRow, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i])
	}
	c.JSON(http.StatusOK, gin.H{"deck_ids": deckIDs, "cards": out, "count": len(out)})
}

// exportRows 一次性取出导出数据；M4-5 会把它改成流式以支持超大卡组。
func (a *API) exportRows(ctx context.Context, userID uint64, deckIDs []uint64, includeProgress bool) ([]exportRow, error) {
	if len(deckIDs) == 0 {
		return []exportRow{}, nil
	}
	type scanRow struct {
		CardID      uint64
		NoteID      uint64
		DeckID      uint64
		Kind        string
		Template    string
		FieldsJSON  string
		TagsJSON    string
		ExternalRef *string
		State       *string
		DueAt       *time.Time
		Reps        *int
		Lapses      *int
	}
	var raw []scanRow
	q := a.db.WithContext(ctx).Table("cards").
		Select(`cards.id AS card_id, cards.note_id AS note_id, notes.deck_id AS deck_id,
		        notes.kind AS kind, cards.template AS template, notes.fields_json AS fields_json,
		        notes.tags_json AS tags_json, notes.external_ref AS external_ref,
		        card_states.state AS state, card_states.due_at AS due_at,
		        card_states.reps AS reps, card_states.lapses AS lapses`).
		Joins("JOIN notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Where("cards.deleted_at IS NULL").
		Where("notes.deck_id IN ?", deckIDs)
	if includeProgress {
		q = q.Joins("LEFT JOIN card_states ON card_states.card_id = cards.id AND card_states.user_id = ?", userID)
	}
	if err := q.Scan(&raw).Error; err != nil {
		return nil, err
	}
	rows := make([]exportRow, 0, len(raw))
	for _, r := range raw {
		fields, err := store.ParseFields(r.FieldsJSON)
		if err != nil || fields == nil {
			fields = map[string]any{}
		}
		tags, err := store.ParseTags(r.TagsJSON)
		if err != nil || tags == nil {
			tags = []string{}
		}
		rows = append(rows, exportRow{
			CardID: r.CardID, NoteID: r.NoteID, DeckID: r.DeckID,
			Kind: r.Kind, Template: r.Template, Fields: fields, Tags: tags,
			ExternalRef: r.ExternalRef,
			State:       r.State, DueAt: r.DueAt, Reps: r.Reps, Lapses: r.Lapses,
		})
	}
	return rows, nil
}

// writeCSV 以 CSV 输出卡片级数据；include_progress 决定是否带进度列。
func (a *API) writeCSV(c *gin.Context, rows []exportRow, includeProgress bool) {
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Status(http.StatusOK)
	w := csv.NewWriter(c.Writer)
	header := []string{"card_id", "note_id", "deck_id", "kind", "template", "fields_json", "tags_json", "external_ref"}
	if includeProgress {
		header = append(header, "state", "due_at", "reps", "lapses")
	}
	_ = w.Write(header)
	for _, r := range rows {
		fieldsJSON, _ := json.Marshal(r.Fields)
		tagsJSON, _ := json.Marshal(r.Tags)
		ref := ""
		if r.ExternalRef != nil {
			ref = *r.ExternalRef
		}
		rec := []string{
			strconv.FormatUint(r.CardID, 10), strconv.FormatUint(r.NoteID, 10),
			strconv.FormatUint(r.DeckID, 10), r.Kind, r.Template,
			string(fieldsJSON), string(tagsJSON), ref,
		}
		if includeProgress {
			state, due, reps, lapses := "", "", "", ""
			if r.State != nil {
				state = *r.State
			}
			if r.DueAt != nil {
				due = r.DueAt.UTC().Format(time.RFC3339)
			}
			if r.Reps != nil {
				reps = strconv.Itoa(*r.Reps)
			}
			if r.Lapses != nil {
				lapses = strconv.Itoa(*r.Lapses)
			}
			rec = append(rec, state, due, reps, lapses)
		}
		_ = w.Write(rec)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		a.logger.Error("write export csv failed", "error", err)
	}
}
