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

// measurePeak 执行 fn，并返回执行期间堆分配（HeapAlloc）相对执行前基线增长的最大字节数。
// 用后台采样线程读取 MemStats —— 验收要求的\"峰值内存断言\"。
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

// seedBulkNotes 直接写 n 条 note（每条 back 字段约 2KB）与每 note 一张 card，
// 用于制造\"整组读入内存会明显占内存\"的数据量。
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
	// 批量插入后重新取回自增 id，避免依赖驱动是否回填主键。
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

// TestExportTenThousandNotesStreamsWithoutBuffering 是核心验收：
// 导出 1 万条 note 时流式路径的峰值内存显著低于整体收集路径，证明没有把整组读进内存。
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
		buffered, err = env.api.CollectExportRows(ctx, user.ID, []uint64{deck.ID}, false)
		if err != nil {
			t.Fatalf("CollectExportRows() error = %v", err)
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

// TestExportFormatsAndProgressColumns 校验 JSON / CSV 两种格式，以及 include_progress
// 开关是否恰好增删进度列。
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

	// JSON 不带进度：不含 state 键。
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

	// JSON 带进度：state=review、reps=3。
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

	// CSV 不带进度：表头 8 列，无 state。
	status, raw = doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/v1/export?deck=%s&format=csv", deck.PublicID), k.Plaintext, "")
	if status != http.StatusOK {
		t.Fatalf("csv export status = %d, want 200", status)
	}
	csvHeader := strings.SplitN(string(raw), "\n", 2)[0]
	if strings.Contains(csvHeader, "state") {
		t.Errorf("csv header without include_progress = %q, want no state column", csvHeader)
	}

	// CSV 带进度：表头含 state,due_at,reps,lapses，数据行含 review 与 3。
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

	// include_progress 非法值（如 2）按未开启处理，避免静默接受非 0/1。
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

// TestExportRequiresExplicitDeck 断言 GET /api/v1/export 必须显式指定卡组：deck 缺省或为空
// 一律 400；给了一个未知的对外 id 则是 404（与「卡组不存在」同口径）。
// 全库/跨用户导出入口已按 2026-10-06 的决定移除，导出只剩单个显式卡组。
func TestExportRequiresExplicitDeck(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "exp_nodeck", store.RoleUser)
	seedDeck(t, env.db, user.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeRead}, nil)

	// 缺 deck 参数（或值为空）：400 invalid_request。
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

	// deck 给了但不是已知的对外 id：404 not_found。
	status, raw := doJSON(t, env.router(), http.MethodGet,
		"/api/v1/export?deck="+missingPublicID, k.Plaintext, "")
	if status != http.StatusNotFound {
		t.Errorf("GET /api/v1/export with an unknown deck status = %d, want 404 (body %s)", status, raw)
	}
}

// blockingResponseWriter 把响应写入挂在测试指定的那次 Write 上，用来模拟「客户端读得很慢」：
// hold 返回 true 的那次写入先关闭 blocked（通知测试已经挂住），再等 release 放行；后续写入直通。
// 只挂第一次匹配的写入，所以被挂住的那次写入就是被测代码持有数据库连接的整段时间里的一刻。
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

// TestExportReleasesDatabaseConnectionWhileClientWriteIsBlocked 是 FX-17 的核心验收：
// 客户端写出被挂住时，导出不得继续持有数据库连接。
//
// SQLite 部署把连接数压到 1（internal/store/store.go 的 SetMaxOpenConns(1)，写入靠它串行化），
// 所以「导出还占着连接」等价于「同一条句柄上的另一次查询在 2 秒预算内做不完」。
// 夹具 8 行、页大小 2 → 4 页，保证挂住点出现在分页路径上而不是只有一页的退化情形。
//
// 负向对照：把 ExportCards 退回「一条游标直接流式写完」，挂住点仍在第一行写出时，
// 但此时游标没读完、连接没还，这条测试必须变红（并发查询超时）。
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
		// 只在第一行卡片数据写出时挂住：此前的写入只有固定的 JSON 前缀（deck_ids 与 "cards":[），
		// 那时还没有任何数据库交互，挂在那里测不出连接是否被占用。
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
	// 任何提前失败路径都要放行写入并等 handler 收场，避免把 goroutine 与连接留在测试里。
	defer func() {
		release()
		<-done
	}()

	select {
	case <-w.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("export never wrote a card row; the blocking writer was not reached")
	}

	// 客户端被挂住的此刻，从同一条数据库句柄再发一次查询。
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

	// 挂住一次写入不改变对外输出：仍是完整合法的 JSON，8 行、count=8。
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

// TestExportOutputIsIndependentOfPageSize 断言分页不改变对外输出：同一份数据用「一页装下」与
// 「每页 2 行」两种页大小导出，JSON 与 CSV、带与不带 include_progress 的响应体必须逐字节相同，
// 且行顺序按 cards.id 升序（键集分页的推进顺序）。
func TestExportOutputIsIndependentOfPageSize(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "page_size", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeRead}, nil)

	const total = 7
	seedBulkNotes(t, env, deck.ID, total)

	// 前 3 张卡写入进度，覆盖 include_progress 的两种取值。
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
			env.api.exportPageSize = 1000 // 一页装下：与旧实现的一次性游标等价
			_, onePage := doJSON(t, router, http.MethodGet, path, k.Plaintext, "")
			env.api.exportPageSize = 2 // 4 页，逼迫多页推进
			_, manyPages := doJSON(t, router, http.MethodGet, path, k.Plaintext, "")
			if !bytes.Equal(onePage, manyPages) {
				t.Errorf("format=%s include_progress=%q: paged body differs from single-page body:\n single page: %s\n paged:       %s",
					format, prog, onePage, manyPages)
			}
		}
	}

	// 行顺序：按 cards.id 升序，与导出体的 card_id 序列一致。
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
