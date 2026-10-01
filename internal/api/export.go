package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// exportCards 以 json 或 csv 导出卡片级内容；include_progress=1 时附带调用者本人的进度。
// 导出他人共享的卡组时绝不包含他人进度（DESIGN.md §7.6 的同一条边界）。
//
// M4-5：两种格式都边查边写（流式），大卡组不把整组读进内存。查询本身用 Rows() 游标
// 逐行推进，这里只负责把每行直接写进 http.ResponseWriter。
func (a *API) exportCards(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()

	var deckID uint64
	if raw := c.Query("deck"); raw != "" {
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || id == 0 {
			abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
			return
		}
		deckID = id
	}
	format := c.DefaultQuery("format", "json")
	if format != "json" && format != "csv" {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	includeProgress := c.Query("include_progress") == "1"

	// 权限与卡组存在性必须在写任何响应字节之前确定，否则无法回 404/403。
	deckIDs, err := a.ExportDeckIDs(ctx, u.ID, deckID)
	if err != nil {
		writeServiceError(c, err)
		return
	}

	if format == "csv" {
		a.streamCSV(c, ctx, u.ID, deckIDs, includeProgress)
		return
	}
	a.streamJSON(c, ctx, u.ID, deckIDs, includeProgress)
}

// streamJSON 以流式方式写 {"deck_ids":[...],"cards":[...],"count":N}。
// 先写固定前缀，再逐行编码数组元素，最后收尾；中途查询失败时仍把数组闭合，
// 让客户端拿到合法 JSON 并在日志里留下英文错误。
func (a *API) streamJSON(c *gin.Context, ctx context.Context, userID uint64, deckIDs []uint64, includeProgress bool) {
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Status(http.StatusOK)
	w := c.Writer

	deckRaw, _ := json.Marshal(deckIDs)
	_, _ = w.Write([]byte(`{"deck_ids":`))
	_, _ = w.Write(deckRaw)
	_, _ = w.Write([]byte(`,"cards":[`))

	count := 0
	enc := json.NewEncoder(w)
	err := a.ExportCards(ctx, userID, deckIDs, includeProgress, func(r ExportRow) error {
		if count > 0 {
			if _, werr := w.Write([]byte(",")); werr != nil {
				return werr
			}
		}
		if werr := enc.Encode(r); werr != nil {
			return werr
		}
		count++
		return nil
	})
	if err != nil {
		a.logger.Error("stream export json failed", "user_id", userID, "cards_written", count, "error", err)
	}
	_, _ = w.Write([]byte(`],"count":`))
	_, _ = w.Write([]byte(strconv.Itoa(count)))
	_, _ = w.Write([]byte("}"))
}

// streamCSV 以流式方式写 CSV：表头一次写完，数据行由 encoding/csv 的 Writer 直写。
func (a *API) streamCSV(c *gin.Context, ctx context.Context, userID uint64, deckIDs []uint64, includeProgress bool) {
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Status(http.StatusOK)
	w := csv.NewWriter(c.Writer)
	_ = w.Write(csvHeader(includeProgress))
	err := a.ExportCards(ctx, userID, deckIDs, includeProgress, func(r ExportRow) error {
		return w.Write(csvRecord(r, includeProgress))
	})
	if err != nil {
		a.logger.Error("stream export csv failed", "user_id", userID, "error", err)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		a.logger.Error("write export csv failed", "user_id", userID, "error", err)
	}
}

// csvHeader 返回 CSV 表头；include_progress 决定是否带进度列（与 JSON 导出一致）。
func csvHeader(includeProgress bool) []string {
	header := []string{"card_id", "note_id", "deck_id", "kind", "template", "fields_json", "tags_json", "external_ref"}
	if includeProgress {
		header = append(header, "state", "due_at", "reps", "lapses")
	}
	return header
}

// csvRecord 把一行导出数据转成 CSV 记录。
func csvRecord(r ExportRow, includeProgress bool) []string {
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
	return rec
}
