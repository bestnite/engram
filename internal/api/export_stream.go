package api

import (
	"context"
	"net/http"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// Streaming export. It lives in its own file, separate from the bulk import internals,
// though both sit in the service layer and are shared by REST handlers and MCP tools.

// exportDefaultPageSize is the default page size for export paging: the cursor closes as soon as a page is read and the connection is idle between pages.
// The page size is at once three bounds: the memory of one page, how long a database connection is held, and the cost of one query per page.
// 500 rows is enough to amortize the one-query-per-page cost while keeping peak memory for exporting 10k notes independent of the total.
const exportDefaultPageSize = 500

// exportScanRow is one raw row of columns from the export cursor; the field names match the SELECT list one-to-one and GORM maps them by column name.
// The id columns take each entity's public_id (external id), no longer the auto-increment primary key.
// card_pk is the exception: it is cards' auto-increment primary key, used only to advance keyset paging and never exposed to callers,
// hence it is not part of ExportRow.
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

// exportPageRow is one row within a page: the external shape plus the auto-increment primary key used to advance to the next page.
type exportPageRow struct {
	cardPK uint64
	out    ExportRow
}

// ExportCards reads card-level export data page by page via a callback: take a page (closing the cursor once read), hand that page row by row to
// emit, then take the next page; an error from emit aborts at once (so a disconnected HTTP client or a failed write can end early).
//
// Paging rather than draining a single cursor is because "a cursor not read to the end" equals "a connection not returned": the SQLite deployment caps connections at 1,
// and writes are serialized on that (see internal/store/store.go). If one cursor streamed rows straight at the http.ResponseWriter,
// a slow-reading client (slow link or a deliberately slow read) would occupy the single connection for the whole export,
// while every other database access in the service queues in the pool. With paging the connection is held only while "fetching a page",
// and it is idle for the entire time the response body is written.
//
// Memory is still bounded: at most one page (API.exportPageSize rows) is in memory at any moment, independent of the number of exported rows.
// Use CollectExportRows when a whole slice is needed (MCP tools must return the complete JSON in one go).
func (a *API) ExportCards(ctx context.Context, userID uint64, deckIDs []uint64, includeProgress bool, emit func(ExportRow) error) error {
	if len(deckIDs) == 0 {
		return nil
	}
	pageSize := a.exportPageSize
	if pageSize <= 0 {
		pageSize = exportDefaultPageSize
	}
	// Keyset paging rather than LIMIT/OFFSET: advancing monotonically on ascending cards.id means
	// cards inserted or deleted during the export never cause rows to be skipped or duplicated, at the cost of one JOINed query per page.
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

// exportCardPage reads one page of export data and closes the cursor before returning. When the caller gets the result the connection is already back in the pool,
// so it is not held for the entire subsequent write of the response body.
func (a *API) exportCardPage(ctx context.Context, userID uint64, deckIDs []uint64, includeProgress bool, afterID uint64, limit int) ([]exportPageRow, error) {
	// Progress columns are selected only when include_progress is set; without progress, card_states is not referenced,
	// otherwise SQL reports "no such column" for the missing JOIN.
	// All three id columns take each table's public_id (external id); deck needs an extra JOIN on decks to get its external id.
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
	// Safety-net close; every return path below closes explicitly once more, returning the connection to the pool as early as possible (Close is repeatable).
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
	// Close the cursor explicitly: leaving it open means holding the connection until the whole export ends.
	rows.Close()
	if iterErr != nil {
		a.logger.Error("iterate export rows failed", "user_id", userID, "error", iterErr)
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to export cards")
	}
	return page, nil
}

// CollectExportRows collects the streaming export into a slice; only for callers that must return complete JSON (MCP export_deck).
// HTTP export goes through ExportCards' streaming path, not through here.
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
