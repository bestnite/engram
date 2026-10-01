package api

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// exportCards 以 json 或 csv 导出卡片级内容；include_progress=1 时附带调用者本人的进度。
// 导出他人共享的卡组时绝不包含他人进度（DESIGN.md §7.6 的同一条边界）。
func (a *API) exportCards(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()

	var deckID uint64
	if raw := c.Query("deck"); raw != "" {
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || id == 0 {
			abortError(c, http.StatusBadRequest, CodeInvalidRequest, "deck must be a positive integer")
			return
		}
		deckID = id
	}
	format := c.DefaultQuery("format", "json")
	if format != "json" && format != "csv" {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "format must be json or csv")
		return
	}
	includeProgress := c.Query("include_progress") == "1"

	deckIDs, err := a.ExportDeckIDs(ctx, u.ID, deckID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	rows, err := a.ExportCards(ctx, u.ID, deckIDs, includeProgress)
	if err != nil {
		a.logger.Error("export query failed", "user_id", u.ID, "error", err)
		writeServiceError(c, err)
		return
	}

	if format == "csv" {
		a.writeCSV(c, rows, includeProgress)
		return
	}
	out := make([]ExportRow, 0, len(rows))
	out = append(out, rows...)
	c.JSON(http.StatusOK, gin.H{"deck_ids": deckIDs, "cards": out, "count": len(out)})
}

// writeCSV 以 CSV 输出卡片级数据；include_progress 决定是否带进度列。
func (a *API) writeCSV(c *gin.Context, rows []ExportRow, includeProgress bool) {
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
