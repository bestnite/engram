package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// exportCards exports the card-level content of a single deck as json or csv; include_progress=1 also carries the caller's own progress.
// When exporting a deck someone else shared, their progress is never included (same boundary).
//
// Both formats query and write as they go (streaming), so a large deck is not read fully into memory: the query advances page by page (keyset),
// the cursor is closed as soon as a page is read, and this code only writes each page's rows straight into the http.ResponseWriter.
// No database connection is held between pages, so a slow client cannot tie up SQLite's single connection.
//
// deck must be given explicitly: the whole-library / cross-user export entry point was removed by the 2026-10-06 decision, leaving single-deck export only.
// The deck value is the deck's external id; it is resolved to a primary key before entering the numeric service layer.
func (a *API) exportCards(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()

	raw := strings.TrimSpace(c.Query("deck"))
	if raw == "" {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	format := c.DefaultQuery("format", "json")
	if format != "json" && format != "csv" {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	includeProgress := c.Query("include_progress") == "1"

	d, err := a.decks.ByPublicID(ctx, raw)
	if err != nil {
		abortNotFound(c)
		return
	}
	// Permission and deck existence must be settled before writing any response byte, otherwise 404/403 can no longer be returned.
	if _, err := a.RequireDeckRole(ctx, u.ID, d.ID, store.RoleReader); err != nil {
		writeServiceError(c, err)
		return
	}
	deckIDs := []uint64{d.ID}

	if format == "csv" {
		a.streamCSV(c, ctx, u.ID, deckIDs, includeProgress)
		return
	}
	a.streamJSON(c, ctx, u.ID, []string{d.PublicID}, deckIDs, includeProgress)
}

// streamJSON writes {"deck_ids":[...],"cards":[...],"count":N} in streaming fashion.
// deck_ids holds the deck's external id, not the auto-increment primary key.
// Write the fixed prefix first, then encode array elements one by one, then close; if a query fails midway the array is still closed,
// so the client gets valid JSON and the English error still lands in the log.
func (a *API) streamJSON(c *gin.Context, ctx context.Context, userID uint64, deckPublicIDs []string, deckIDs []uint64, includeProgress bool) {
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Status(http.StatusOK)
	w := c.Writer

	deckRaw, _ := json.Marshal(deckPublicIDs)
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

// streamCSV writes CSV in streaming fashion: the header is written in one go and data rows go straight through encoding/csv's Writer.
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

// csvHeader returns the CSV header; include_progress decides whether progress columns are present (consistent with the JSON export).
func csvHeader(includeProgress bool) []string {
	header := []string{"card_id", "note_id", "deck_id", "kind", "template", "fields_json", "tags_json", "external_ref"}
	if includeProgress {
		header = append(header, "state", "due_at", "reps", "lapses")
	}
	return header
}

// csvRecord turns one exported row into a CSV record.
func csvRecord(r ExportRow, includeProgress bool) []string {
	fieldsJSON, _ := json.Marshal(r.Fields)
	tagsJSON, _ := json.Marshal(r.Tags)
	ref := ""
	if r.ExternalRef != nil {
		ref = *r.ExternalRef
	}
	rec := []string{
		r.CardID, r.NoteID, r.DeckID, r.Kind, r.Template,
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
