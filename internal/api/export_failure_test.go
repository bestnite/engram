package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// seedExportDeck writes one note and one card into a fresh deck and returns the deck. fieldSize pads the
// note's back field, which a CSV test uses to make encoding/csv flush a row before the next page is read.
func seedExportDeck(t *testing.T, env *testEnv, fieldSize int) *store.Deck {
	t.Helper()
	user := seedUser(t, env.db, "exporter_failure", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	now := time.Now().UTC()
	n := store.Note{
		DeckID:     deck.ID,
		NoteTypeID: store.Ptr(stockNoteTypeID(t, env.db, deck.ID, "basic")),
		FieldsJSON: fmt.Sprintf(`{"front":"q","back":%q}`, strings.Repeat("x", fieldSize)),
		TagsJSON:   "[]",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := env.db.Create(&n).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	if err := env.db.Create(&store.Card{NoteID: n.ID, Template: "forward", CreatedAt: now}).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	return deck
}

// exportTestContext builds the bare gin context a streaming handler needs, so a test can call the handler
// directly and fail a page read at a chosen point.
func exportTestContext(t *testing.T, target string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	return c, rec
}

// closeOnFirstWrite closes the database on the response's first write: the page holding the row in hand has
// already been read, so that row is still written and the next page's read is the one that fails.
type closeOnFirstWrite struct {
	gin.ResponseWriter
	once  sync.Once
	close func()
}

func (w *closeOnFirstWrite) Write(b []byte) (int, error) {
	w.once.Do(w.close)
	return w.ResponseWriter.Write(b)
}

// TestExportFailureBeforeFirstRowIsAnError asserts a page read that fails before any row is written answers the
// 5xx error envelope instead of a 200 body that reads as a valid empty export.
func TestExportFailureBeforeFirstRowIsAnError(t *testing.T) {
	for _, format := range []string{"json", "csv"} {
		t.Run(format, func(t *testing.T) {
			env := newTestEnv(t, 60, 60)
			deck := seedExportDeck(t, env, 0)
			sqlDB, err := env.db.DB()
			if err != nil {
				t.Fatalf("underlying db: %v", err)
			}
			if err := sqlDB.Close(); err != nil {
				t.Fatalf("close db: %v", err)
			}

			c, rec := exportTestContext(t, "/api/v1/export")
			switch format {
			case "json":
				env.api.streamJSON(c, context.Background(), deck.OwnerUserID, []string{deck.PublicID}, []uint64{deck.ID}, false)
			case "csv":
				env.api.streamCSV(c, context.Background(), deck.OwnerUserID, []uint64{deck.ID}, false)
			}

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500 (body %q)", rec.Code, rec.Body.String())
			}
			var envelope struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("body is not the error envelope: %v (%q)", err, rec.Body.String())
			}
			if envelope.Error.Code != CodeInternal {
				t.Errorf("error code = %q, want %q", envelope.Error.Code, CodeInternal)
			}
			if body := rec.Body.String(); strings.Contains(body, "cards") || strings.Contains(body, "card_id") || strings.Contains(body, "truncated") {
				t.Errorf("failed export body = %q, want no export content", body)
			}
		})
	}
}

// TestExportFailureAfterFirstRowIsMarked asserts a page read that fails after a row was written keeps the 200
// body, keeps the rows already sent, and marks the export as cut short.
func TestExportFailureAfterFirstRowIsMarked(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		env := newTestEnv(t, 60, 60)
		deck := seedExportDeck(t, env, 0)
		env.api.exportPageSize = 1
		sqlDB, err := env.db.DB()
		if err != nil {
			t.Fatalf("underlying db: %v", err)
		}

		c, rec := exportTestContext(t, "/api/v1/export")
		c.Writer = &closeOnFirstWrite{ResponseWriter: c.Writer, close: func() { _ = sqlDB.Close() }}
		env.api.streamJSON(c, context.Background(), deck.OwnerUserID, []string{deck.PublicID}, []uint64{deck.ID}, false)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
		var body struct {
			Cards     []map[string]any `json:"cards"`
			Count     int              `json:"count"`
			Truncated bool             `json:"truncated"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("body is not valid JSON: %v (%q)", err, rec.Body.String())
		}
		if len(body.Cards) != 1 || body.Count != 1 {
			t.Errorf("cards = %d, count = %d, want 1/1: the row already read must survive", len(body.Cards), body.Count)
		}
		if !body.Truncated {
			t.Error("truncated = false, want true: the export stopped before the last page")
		}
	})

	t.Run("csv", func(t *testing.T) {
		env := newTestEnv(t, 60, 60)
		// A row larger than encoding/csv's buffer makes the writer flush during the row, which is where this test
		// closes the database: the next page's read is then the failure.
		deck := seedExportDeck(t, env, 8000)
		env.api.exportPageSize = 1
		sqlDB, err := env.db.DB()
		if err != nil {
			t.Fatalf("underlying db: %v", err)
		}

		c, rec := exportTestContext(t, "/api/v1/export")
		c.Writer = &closeOnFirstWrite{ResponseWriter: c.Writer, close: func() { _ = sqlDB.Close() }}
		env.api.streamCSV(c, context.Background(), deck.OwnerUserID, []uint64{deck.ID}, false)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
		lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
		if len(lines) != 3 {
			t.Fatalf("lines = %d, want header, one row and the truncation marker (%q)", len(lines), rec.Body.String())
		}
		if lines[len(lines)-1] != "#export_truncated" {
			t.Errorf("last line = %q, want the truncation marker %q", lines[len(lines)-1], "#export_truncated")
		}
	})
}
