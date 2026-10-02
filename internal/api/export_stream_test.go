package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"example.com/engram/internal/store"
)

// measurePeak 执行 fn，并返回执行期间堆分配（HeapAlloc）相对执行前基线增长的最大字节数。
// 用后台采样线程读取 MemStats —— M4-5 验收要求的\"峰值内存断言\"。
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

// TestExportTenThousandNotesStreamsWithoutBuffering 是 M4-5 的核心验收：
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
	status, raw := doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/v1/export?deck=%d&format=json", deck.ID), k.Plaintext, "")
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
		fmt.Sprintf("/api/v1/export?deck=%d&format=json&include_progress=1", deck.ID), k.Plaintext, "")
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
	status, raw = doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/v1/export?deck=%d&format=csv", deck.ID), k.Plaintext, "")
	if status != http.StatusOK {
		t.Fatalf("csv export status = %d, want 200", status)
	}
	csvHeader := strings.SplitN(string(raw), "\n", 2)[0]
	if strings.Contains(csvHeader, "state") {
		t.Errorf("csv header without include_progress = %q, want no state column", csvHeader)
	}

	// CSV 带进度：表头含 state,due_at,reps,lapses，数据行含 review 与 3。
	status, raw = doJSON(t, router, http.MethodGet,
		fmt.Sprintf("/api/v1/export?deck=%d&format=csv&include_progress=1", deck.ID), k.Plaintext, "")
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
		fmt.Sprintf("/api/v1/export?deck=%d&format=json&include_progress=2", deck.ID), k.Plaintext, "")
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
