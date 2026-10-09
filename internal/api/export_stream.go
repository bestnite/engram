package api

import (
	"context"
	"net/http"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// 流式导出。放在独立文件里，与批量导入的内部实现分开，
// 两者都在 service 层，REST handler 与 MCP 工具共用。

// exportDefaultPageSize 是导出分页的默认页大小：每页读满即关闭游标，页与页之间连接是空闲的。
// 页大小同时是三个上界：一页的内存、一条数据库连接被占用的时长、每页一次查询的开销。
// 500 行足以摊薄每页一次查询的成本，同时让导出 1 万条 note 的峰值内存不随总数增长。
const exportDefaultPageSize = 500

// exportScanRow 是导出游标的一行原始列；字段名与 SELECT 列表一一对应，由 GORM 按列名映射。
// id 列取各实体的 public_id（对外 id），不再取自增主键。
// card_pk 是例外：它是 cards 的自增主键，只用来推进键集分页，不对调用方暴露，
// 因此不进 ExportRow。
type exportScanRow struct {
	CardPK      uint64
	CardID      string
	NoteID      string
	DeckID      string
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

// exportPageRow 是一页里的一行：对外的形态，外加推进下一页用的自增主键。
type exportPageRow struct {
	cardPK uint64
	out    ExportRow
}

// ExportCards 以回调方式分页读出卡片级导出数据：取一页（读完即关闭游标），把这一页逐行交给
// emit，再取下一页；emit 返回错误时立即中止（供 HTTP 客户端断开或写入失败时提前收场）。
//
// 分页而不是一条游标读到底，是因为「游标不读完」等于「连接不还」：SQLite 部署把连接数压到 1，
// 写入靠这一点串行化（见 internal/store/store.go）。若一条游标直接对着 http.ResponseWriter
// 逐行写，客户端读得慢（慢速连接或恶意慢读）就会让唯一的连接被占住整段导出时间，
// 期间服务里所有其它数据库访问都在连接池里排队。分页后连接只在「取一页」期间被占用，
// 写响应体的整段时间里连接是空闲的。
//
// 内存仍有界：任意时刻最多一页（API.exportPageSize 行）在内存里，不随导出行数增长。
// 需要整体切片时用 CollectExportRows（MCP 工具必须一次返回完整 JSON）。
func (a *API) ExportCards(ctx context.Context, userID uint64, deckIDs []uint64, includeProgress bool, emit func(ExportRow) error) error {
	if len(deckIDs) == 0 {
		return nil
	}
	pageSize := a.exportPageSize
	if pageSize <= 0 {
		pageSize = exportDefaultPageSize
	}
	// 键集（keyset）分页，而不是 LIMIT/OFFSET：按 cards.id 升序单调推进，
	// 导出期间插入或删除卡片不会让某些行被跳过或重复，代价是每页一次带 JOIN 的查询。
	var lastID uint64
	for {
		page, err := a.exportCardPage(ctx, userID, deckIDs, includeProgress, lastID, pageSize)
		if err != nil {
			return err
		}
		for _, row := range page {
			if err := emit(row.out); err != nil {
				return err
			}
		}
		if len(page) < pageSize {
			return nil
		}
		lastID = page[len(page)-1].cardPK
	}
}

// exportCardPage 读一页导出数据并在返回前关闭游标。调用方拿到返回值时这条连接已经还回连接池，
// 所以随后写响应体的整段时间都不持有它。
func (a *API) exportCardPage(ctx context.Context, userID uint64, deckIDs []uint64, includeProgress bool, afterID uint64, limit int) ([]exportPageRow, error) {
	// 进度列只在 include_progress 时选取；未选进度时不引用 card_states，
	// 否则 SQL 会因缺少该 JOIN 而报 “no such column”。
	// 三个 id 列都取各表的 public_id（对外 id）；deck 需要额外 JOIN decks 才能拿到它的对外 id。
	selectCols := `cards.id AS card_pk, cards.public_id AS card_id, notes.public_id AS note_id,
	        decks.public_id AS deck_id, notes.kind AS kind, cards.template AS template,
	        notes.fields_json AS fields_json, notes.tags_json AS tags_json,
	        notes.external_ref AS external_ref`
	q := a.db.WithContext(ctx).Table("cards").
		Joins("JOIN notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Joins("JOIN decks ON decks.id = notes.deck_id").
		Where("cards.deleted_at IS NULL").
		Where("notes.deck_id IN ?", deckIDs).
		Where("cards.id > ?", afterID)
	if includeProgress {
		selectCols += `, card_states.state AS state, card_states.due_at AS due_at,
		        card_states.reps AS reps, card_states.lapses AS lapses`
		q = q.Joins("LEFT JOIN card_states ON card_states.card_id = cards.id AND card_states.user_id = ?", userID)
	}

	rows, err := q.Select(selectCols).Order("cards.id ASC").Limit(limit).Rows()
	if err != nil {
		a.logger.Error("export query failed", "user_id", userID, "error", err)
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to export cards")
	}
	// 兜底关闭；下面每条返回路径都再显式 Close 一次，把连接尽早还给连接池（Close 可重复调用）。
	defer rows.Close()

	page := make([]exportPageRow, 0, limit)
	for rows.Next() {
		var r exportScanRow
		if err := a.db.ScanRows(rows, &r); err != nil {
			rows.Close()
			a.logger.Error("scan export row failed", "user_id", userID, "error", err)
			return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to export cards")
		}
		page = append(page, exportPageRow{
			cardPK: r.CardPK,
			out: ExportRow{
				CardID: r.CardID, NoteID: r.NoteID, DeckID: r.DeckID,
				Kind: r.Kind, Template: r.Template,
				Fields: store.FieldsOrEmpty(r.FieldsJSON), Tags: store.TagsOrEmpty(r.TagsJSON),
				ExternalRef: r.ExternalRef,
				State:       r.State, DueAt: r.DueAt, Reps: r.Reps, Lapses: r.Lapses,
			},
		})
	}
	iterErr := rows.Err()
	// 显式关游标：不关就等于把连接占着，直到整次导出结束。
	rows.Close()
	if iterErr != nil {
		a.logger.Error("iterate export rows failed", "user_id", userID, "error", iterErr)
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to export cards")
	}
	return page, nil
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
