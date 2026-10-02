package api

import (
	"context"
	"net/http"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// 流式导出（M4-5）。放在独立文件里，与批量导入（M4-4）的内部实现分开，
// 两者都在 service 层，REST handler 与 MCP 工具共用。

// exportScanRow 是导出游标的一行原始列；字段名与 SELECT 列表一一对应，由 GORM 按列名映射。
type exportScanRow struct {
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

// ExportCards 以回调方式流式读出卡片级导出数据：逐行解析后调用 emit，emit 返回错误时立即中止
// （供 HTTP 客户端断开或写入失败时提前收场）。
//
// M4-5 的关键：用 Rows() 游标逐行扫描，任何时刻内存里只有当前一行的列与解析后的 ExportRow，
// 不把整组读进内存，所以导出 1 万条 note 时峰值内存不随总数线性增长。需要整体切片时用
// CollectExportRows（MCP 工具必须一次返回完整 JSON）。
func (a *API) ExportCards(ctx context.Context, userID uint64, deckIDs []uint64, includeProgress bool, emit func(ExportRow) error) error {
	if len(deckIDs) == 0 {
		return nil
	}
	// 进度列只在 include_progress 时选取；未选进度时不引用 card_states，
	// 否则 SQL 会因缺少该 JOIN 而报 “no such column”。
	selectCols := `cards.id AS card_id, cards.note_id AS note_id, notes.deck_id AS deck_id,
	        notes.kind AS kind, cards.template AS template, notes.fields_json AS fields_json,
	        notes.tags_json AS tags_json, notes.external_ref AS external_ref`
	q := a.db.WithContext(ctx).Table("cards").
		Joins("JOIN notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Where("cards.deleted_at IS NULL").
		Where("notes.deck_id IN ?", deckIDs)
	if includeProgress {
		selectCols += `, card_states.state AS state, card_states.due_at AS due_at,
		        card_states.reps AS reps, card_states.lapses AS lapses`
		q = q.Joins("LEFT JOIN card_states ON card_states.card_id = cards.id AND card_states.user_id = ?", userID)
	}

	rows, err := q.Select(selectCols).Rows()
	if err != nil {
		a.logger.Error("export query failed", "user_id", userID, "error", err)
		return newServiceError(http.StatusInternalServerError, CodeInternal, "failed to export cards")
	}
	defer rows.Close()

	for rows.Next() {
		var r exportScanRow
		if err := a.db.ScanRows(rows, &r); err != nil {
			a.logger.Error("scan export row failed", "user_id", userID, "error", err)
			return newServiceError(http.StatusInternalServerError, CodeInternal, "failed to export cards")
		}
		if err := emit(ExportRow{
			CardID: r.CardID, NoteID: r.NoteID, DeckID: r.DeckID,
			Kind: r.Kind, Template: r.Template,
			Fields: exportFields(r.FieldsJSON), Tags: exportTags(r.TagsJSON),
			ExternalRef: r.ExternalRef,
			State:       r.State, DueAt: r.DueAt, Reps: r.Reps, Lapses: r.Lapses,
		}); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		a.logger.Error("iterate export rows failed", "user_id", userID, "error", err)
		return newServiceError(http.StatusInternalServerError, CodeInternal, "failed to export cards")
	}
	return nil
}

// CollectExportRows 把流式导出收集成切片；只给必须整体返回 JSON 的调用方使用（MCP export_deck）。
// HTTP 导出走 ExportCards 的流式路径，不经过这里。
func (a *API) CollectExportRows(ctx context.Context, userID uint64, deckIDs []uint64, includeProgress bool) ([]ExportRow, error) {
	rows := make([]ExportRow, 0, 64)
	if err := a.ExportCards(ctx, userID, deckIDs, includeProgress, func(r ExportRow) error {
		rows = append(rows, r)
		return nil
	}); err != nil {
		return nil, err
	}
	return rows, nil
}

// exportFields 解码 fields_json；坏数据按空对象处理，导出不因单行坏数据整体失败。
func exportFields(raw string) map[string]any {
	fields, err := store.ParseFields(raw)
	if err != nil || fields == nil {
		return map[string]any{}
	}
	return fields
}

// exportTags 解码 tags_json；坏数据按空数组处理。
func exportTags(raw string) []string {
	tags, err := store.ParseTags(raw)
	if err != nil || tags == nil {
		return []string{}
	}
	return tags
}
