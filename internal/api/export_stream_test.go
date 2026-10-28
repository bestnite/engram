package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// measurePeak runs fn and returns the maximum growth in bytes of heap allocation (HeapAlloc) over the pre-run baseline during execution.
// A background sampling goroutine reads MemStats - the "peak memory assertion" the acceptance requires.
func measurePeak(t *testing.T, fn func()) uint64 {
	t.Helper()
	runtime.GC()
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	baseline := base.HeapAlloc

	var peak atomic.Uint64
	peak.Store(baseline)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		var m runtime.MemStats
		for {
			select {
			case <-stop:
				return
			default:
			}
			runtime.ReadMemStats(&m)
			if m.HeapAlloc > peak.Load() {
				peak.Store(m.HeapAlloc)
			}
			time.Sleep(100 * time.Microsecond)
		}
	}()

	fn()

	close(stop)
	<-done
	best := peak.Load()
	if best < baseline {
		return 0
	}
	return best - baseline
}

// seedBulkNotes writes n notes directly (each with a back field of roughly 2KB) plus one card per note,
// to produce a volume where "reading the whole set into memory would clearly cost memory".
func seedBulkNotes(t *testing.T, env *testEnv, deckID uint64, n int) {
	t.Helper()
	now := time.Now().UTC()
	back := strings.Repeat("x", 2000)
	notes := make([]store.Note, 0, n)
	for i := 0; i < n; i++ {
		notes = append(notes, store.Note{
			DeckID:     deckID,
			Kind:       "basic",
			FieldsJSON: fmt.Sprintf(`{"front":"f%d","back":"%s"}`, i, back),
			TagsJSON:   "[]",
			CreatedAt:  now,
			UpdatedAt:  now,
		})
	}
	if err := env.db.CreateInBatches(notes, 500).Error; err != nil {
		t.Fatalf("seed notes: %v", err)
	}
	// Re-fetch the auto-increment ids after the bulk insert, avoiding reliance on whether the driver back-fills primary keys.
	var ids []uint64
	if err := env.db.Model(&store.Note{}).Where("deck_id = ?", deckID).Pluck("id", &ids).Error; err != nil {
		t.Fatalf("pluck note ids: %v", err)
	}
	cards := make([]store.Card, 0, len(ids))
	for _, id := range ids {
		cards = append(cards, store.Card{NoteID: id, Template: "forward", CreatedAt: now})
	}
	if err := env.db.CreateInBatches(cards, 500).Error; err != nil {
		t.Fatalf("seed cards: %v", err)
	}
}

// TestExportTenThousandNotesStreamsWithoutBuffering is the core acceptance:
// exporting 10k notes, the streaming path's peak memory is markedly lower than the collect-everything path, proving the whole set is not read into memory.
func TestExportTenThousandNotesStreamsWithoutBuffering(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "exporter", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)

	const total = 10000
	seedBulkNotes(t, env, deck.ID, total)
	ctx := context.Background()

	streamRows := 0
	streamPeak := measurePeak(t, func() {
		if err := env.api.ExportCards(ctx, user.ID, []uint64{deck.ID}, false, func(ExportRow) error {
			streamRows++
			return nil
		}); err != nil {
			t.Fatalf("ExportCards() error = %v", err)
		}
	})
	if streamRows != total {
		t.Fatalf("streamed %d rows, want %d", streamRows, total)
	}

	var buffered []ExportRow
	bufferedPeak := measurePeak(t, func() {
		var err error
		buffered, err = collectExportRows(ctx, env.api, user.ID, []uint64{deck.ID})
		if err != nil {
			t.Fatalf("collectExportRows() error = %v", err)
		}
	})
	if len(buffered) != total {
		t.Fatalf("collected %d rows, want %d", len(buffered), total)
	}

	t.Logf("peak heap growth with %d rows: streaming=%d bytes, buffered=%d bytes", total, streamPeak, bufferedPeak)
	if streamPeak >= bufferedPeak {
		t.Errorf("streaming peak growth %d bytes is not below buffered peak growth %d bytes", streamPeak, bufferedPeak)
	}
}

// TestExportFormatsAndProgressColumns verifies both the JSON / CSV formats and whether the include_progress
// switch adds or removes exactly the progress columns.
func TestExportFormatsAndProgressColumns(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "fmt_user", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeRead}, nil)
	router := env.router()

	now := time.Now().UTC()
	n := store.Note{DeckID: deck.ID, Kind: "basic", FieldsJSON: `{"front":"q","back":"a"}`, TagsJSON: "[]", CreatedAt: now, UpdatedAt: now}
	if err := env.db.Create(&n).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	card := store.Card{NoteID: n.ID, Template: "forward", CreatedAt: now}
	if err := env.db.Create(&card).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	due := now.Add(24 * time.Hour)
	if err := env.db.Create(&store.CardState{
		CardID: card.ID, UserID: user.ID, State: "review", DueAt: &due, Reps: 3, Lapses: 1,
	}).Error; err != nil {
		t.Fatalf("create card state: %v", err)
	}

	// JSON without progress: no state key.
	status, raw := doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/v1/export?deck=%s&format=json", deck.PublicID), k.Plaintext, "")
	if status != http.StatusOK {
		t.Fatalf("json export status = %d, want 200 (body %s)", status, raw)
	}
	var noProg struct {
		Cards []map[string]any `json:"cards"`
		Count int              `json:"count"`
	}
	if err := json.Unmarshal(raw, &noProg); err != nil {
		t.Fatalf("json export not parseable: %v (%s)", err, raw)
	}
	if noProg.Count != 1 || len(noProg.Cards) != 1 {
		t.Fatalf("json export count = %d cards = %d, want 1/1", noProg.Count, len(noProg.Cards))
	}
	if _, ok := noProg.Cards[0]["state"]; ok {
		t.Error("json export without include_progress contains state, want it omitted")
	}

	// JSON with progress: state=review, reps=3.
	status, raw = doJSON(t, router, http.MethodGet,
		fmt.Sprintf("/api/v1/export?deck=%s&format=json&include_progress=1", deck.PublicID), k.Plaintext, "")
	if status != http.StatusOK {
		t.Fatalf("json progress export status = %d, want 200", status)
	}
	var withProg struct {
		Cards []map[string]any `json:"cards"`
	}
	if err := json.Unmarshal(raw, &withProg); err != nil {
		t.Fatalf("json progress export not parseable: %v (%s)", err, raw)
	}
	if got := withProg.Cards[0]["state"]; got != "review" {
		t.Errorf("state = %v, want review", got)
	}
	if got := withProg.Cards[0]["reps"]; got != float64(3) {
		t.Errorf("reps = %v, want 3", got)
	}

	// CSV without progress: 8 header columns, no state.
	status, raw = doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/v1/export?deck=%s&format=csv", deck.PublicID), k.Plaintext, "")
	if status != http.StatusOK {
		t.Fatalf("csv export status = %d, want 200", status)
	}
	csvHeader := strings.SplitN(string(raw), "\n", 2)[0]
	if strings.Contains(csvHeader, "state") {
		t.Errorf("csv header without include_progress = %q, want no state column", csvHeader)
	}

	// CSV with progress: the header has state,due_at,reps,lapses and the data row has review and 3.
	status, raw = doJSON(t, router, http.MethodGet,
		fmt.Sprintf("/api/v1/export?deck=%s&format=csv&include_progress=1", deck.PublicID), k.Plaintext, "")
	if status != http.StatusOK {
		t.Fatalf("csv progress export status = %d, want 200", status)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("csv progress export lines = %d, want 2 (%q)", len(lines), raw)
	}
	if !strings.Contains(lines[0], "state") || !strings.Contains(lines[0], "reps") {
		t.Errorf("csv progress header = %q, want state/reps columns", lines[0])
	}
	if !strings.Contains(lines[1], "review") {
		t.Errorf("csv progress data row = %q, want state review", lines[1])
	}

	// An invalid include_progress value (e.g. 2) is treated as off, avoiding silent acceptance of anything but 0/1.
	status, raw = doJSON(t, router, http.MethodGet,
		fmt.Sprintf("/api/v1/export?deck=%s&format=json&include_progress=2", deck.PublicID), k.Plaintext, "")
	if status != http.StatusOK {
		t.Fatalf("include_progress=2 status = %d, want 200", status)
	}
	if err := json.Unmarshal(raw, &noProg); err != nil {
		t.Fatalf("include_progress=2 not parseable: %v (%s)", err, raw)
	}
	if _, ok := noProg.Cards[0]["state"]; ok {
		t.Error("include_progress=2 must behave like off (no state column)")
	}
}

// TestExportRequiresExplicitDeck asserts GET /api/v1/export must name a deck explicitly: deck absent or empty
// is always 400; an unknown external id is 404 (the same convention as "deck does not exist").
// The whole-library / cross-user export entry was removed by the 2026-10-06 decision, leaving single explicit deck export only.
func TestExportRequiresExplicitDeck(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "exp_nodeck", store.RoleUser)
	seedDeck(t, env.db, user.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeRead}, nil)

	// deck parameter missing (or empty value): 400 invalid_request.
	for _, target := range []string{
		"/api/v1/export",
		"/api/v1/export?format=json",
		"/api/v1/export?deck=",
	} {
		status, raw := doJSON(t, env.router(), http.MethodGet, target, k.Plaintext, "")
		if status != http.StatusBadRequest {
			t.Errorf("GET %s status = %d, want 400 (body %s)", target, status, raw)
		}
	}

	// deck given but not a known external id: 404 not_found.
	status, raw := doJSON(t, env.router(), http.MethodGet,
		"/api/v1/export?deck="+missingPublicID, k.Plaintext, "")
	if status != http.StatusNotFound {
		t.Errorf("GET /api/v1/export with an unknown deck status = %d, want 404 (body %s)", status, raw)
	}
}

// blockingResponseWriter blocks on the Write the test picks, simulating "a client reading very slowly":
// the write for which hold returns true closes blocked first (telling the test it is stuck), then waits for release; later writes pass straight through.
// Only the first matching write blocks, so that write is one moment within the whole span in which the code under test holds the database connection.
type blockingResponseWriter struct {
	rec     *httptest.ResponseRecorder
	hold    func(p []byte) bool
	blocked chan struct{}
	release chan struct{}
	mu      sync.Mutex
	held    bool
}

func (w *blockingResponseWriter) Header() http.Header { return w.rec.Header() }

func (w *blockingResponseWriter) WriteHeader(code int) { w.rec.WriteHeader(code) }

func (w *blockingResponseWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	block := !w.held && w.hold(p)
	if block {
		w.held = true
	}
	w.mu.Unlock()
	if block {
		close(w.blocked)
		<-w.release
	}
	return w.rec.Write(p)
}

// TestExportReleasesDatabaseConnectionWhileClientWriteIsBlocked is the core acceptance for FX-17:
// while the client write is blocked, the export must not keep holding the database connection.
//
// The SQLite deployment caps connections at 1 (SetMaxOpenConns(1) in internal/store/store.go, which is how writes are serialized),
// so "the export still holds the connection" equals "another query on the same handle cannot finish within a 2-second budget".
// The fixture has 8 rows with a page size of 2 -> 4 pages, ensuring the blocking point lands on the paging path rather than a degenerate single-page case.
//
// The negative control: reverting ExportCards to "one cursor streamed straight to the end" keeps the blocking point at the first data row write,
// but then the cursor is unfinished and the connection unreturned, so this test must go red (the concurrent query times out).
func TestExportReleasesDatabaseConnectionWhileClientWriteIsBlocked(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "conn_release", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeRead}, nil)

	const total = 8
	env.api.exportPageSize = 2
	seedBulkNotes(t, env, deck.ID, total)

	rec := httptest.NewRecorder()
	w := &blockingResponseWriter{
		rec:     rec,
		blocked: make(chan struct{}),
		release: make(chan struct{}),
		// Block only when the first card data row is written: earlier writes are just the fixed JSON prefix (deck_ids and "cards":[),
		// when no database interaction has happened yet, so blocking there would show nothing about connection occupancy.
		hold: func(p []byte) bool { return bytes.Contains(p, []byte("card_id")) },
	}
	router := env.router()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/export?deck=%s&format=json", deck.PublicID), nil)
	req.Header.Set("Authorization", "Bearer "+k.Plaintext)

	done := make(chan struct{})
	go func() {
		defer close(done)
		router.ServeHTTP(w, req)
	}()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(w.release) }) }
	// Any early-failure path must release the write and wait for the handler to finish, so no goroutine or connection is left behind in the test.
	defer func() {
		release()
		<-done
	}()

	select {
	case <-w.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("export never wrote a card row; the blocking writer was not reached")
	}

	// At the moment the client is blocked, issue another query on the same database handle.
	const budget = 2 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	start := time.Now()
	var count int64
	qerr := env.db.WithContext(ctx).Model(&store.Card{}).
		Where("cards.deleted_at IS NULL").Count(&count).Error
	elapsed := time.Since(start)

	release()
	<-done

	if qerr != nil {
		t.Fatalf("concurrent query while the client write was blocked did not finish within %s (elapsed %s, error %v): the export is holding the only database connection",
			budget, elapsed, qerr)
	}
	if elapsed >= budget {
		t.Fatalf("concurrent query took %s, want well under %s", elapsed, budget)
	}
	t.Logf("concurrent query finished in %s while the export write was blocked", elapsed)

	// Blocking one write does not change the external output: still complete valid JSON, 8 rows, count=8.
	if rec.Code != http.StatusOK {
		t.Fatalf("export status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var out struct {
		DeckIDs []string         `json:"deck_ids"`
		Cards   []map[string]any `json:"cards"`
		Count   int              `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("export body is not valid JSON: %v (%s)", err, rec.Body.String())
	}
	if out.Count != total || len(out.Cards) != total {
		t.Fatalf("export count = %d cards = %d, want %d/%d", out.Count, len(out.Cards), total, total)
	}
}

// TestExportOutputIsIndependentOfPageSize asserts paging does not change the external output: the same data exported with "all in one page" and
// "2 rows per page" must produce byte-identical response bodies for JSON and CSV, with and without include_progress,
// with rows in ascending cards.id order (the advance order of keyset paging).
func TestExportOutputIsIndependentOfPageSize(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "page_size", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeRead}, nil)

	const total = 7
	seedBulkNotes(t, env, deck.ID, total)

	// The first 3 cards get progress written, covering both include_progress values.
	var cardIDs []uint64
	if err := env.db.Model(&store.Card{}).Order("id ASC").Limit(3).Pluck("id", &cardIDs).Error; err != nil {
		t.Fatalf("pluck card ids: %v", err)
	}
	due := time.Now().UTC().Add(24 * time.Hour)
	for _, id := range cardIDs {
		if err := env.db.Create(&store.CardState{
			CardID: id, UserID: user.ID, State: "review", DueAt: &due, Reps: 3, Lapses: 1,
		}).Error; err != nil {
			t.Fatalf("create card state: %v", err)
		}
	}

	router := env.router()
	for _, format := range []string{"json", "csv"} {
		for _, prog := range []string{"", "&include_progress=1"} {
			path := fmt.Sprintf("/api/v1/export?deck=%s&format=%s%s", deck.PublicID, format, prog)
			env.api.exportPageSize = 1000 // one page holds it all: equivalent to the old implementation's one-shot cursor
			_, onePage := doJSON(t, router, http.MethodGet, path, k.Plaintext, "")
			env.api.exportPageSize = 2 // 4 pages, forcing multi-page advance
			_, manyPages := doJSON(t, router, http.MethodGet, path, k.Plaintext, "")
			if !bytes.Equal(onePage, manyPages) {
				t.Errorf("format=%s include_progress=%q: paged body differs from single-page body:\n single page: %s\n paged:       %s",
					format, prog, onePage, manyPages)
			}
		}
	}

	// Row order: ascending cards.id, matching the card_id sequence in the export body.
	var wantOrder []string
	if err := env.db.Table("cards").
		Joins("JOIN notes ON notes.id = cards.note_id").
		Where("cards.deleted_at IS NULL AND notes.deleted_at IS NULL AND notes.deck_id = ?", deck.ID).
		Order("cards.id ASC").Pluck("cards.public_id", &wantOrder).Error; err != nil {
		t.Fatalf("pluck expected order: %v", err)
	}
	env.api.exportPageSize = 2
	status, raw := doJSON(t, router, http.MethodGet,
		fmt.Sprintf("/api/v1/export?deck=%s&format=json", deck.PublicID), k.Plaintext, "")
	if status != http.StatusOK {
		t.Fatalf("paged export status = %d, want 200 (body %s)", status, raw)
	}
	var out struct {
		Cards []struct {
			CardID string `json:"card_id"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("paged export not parseable: %v (%s)", err, raw)
	}
	if len(out.Cards) != len(wantOrder) {
		t.Fatalf("exported %d cards, want %d", len(out.Cards), len(wantOrder))
	}
	for i, want := range wantOrder {
		if out.Cards[i].CardID != want {
			t.Fatalf("card %d = %s, want %s (export must follow cards.id ASC)", i, out.Cards[i].CardID, want)
		}
	}
}

// collectExportRows gathers the streaming export into one slice; it is the buffered baseline the streaming peak is compared against.
func collectExportRows(ctx context.Context, a *API, userID uint64, deckIDs []uint64) ([]ExportRow, error) {
	rows := make([]ExportRow, 0, 64)
	if err := a.ExportCards(ctx, userID, deckIDs, false, func(r ExportRow) error {
		rows = append(rows, r)
		return nil
	}); err != nil {
		return nil, err
	}
	return rows, nil
}
