package mcp

import (
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"testing"

	"example.com/engram/internal/store"
)

// ---- M4-7：工具与 REST 行为一致 ----

// TestToolsMatchREST 是 M4-7 的核心验收：每个工具与其同名 REST 路径在同一输入下产出相同结果。
func TestToolsMatchREST(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "user")
	deck := seedDeck(t, db, u.ID)
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite, store.ScopeReview})
	cs := connect(t, ts.URL, key)
	base := ts.URL
	deckPath := fmt.Sprintf("/api/v1/decks/%d/notes", deck.ID)

	// 用 REST 预先建两张内容相同的卡，供后续对比。
	body := `{"notes":[` +
		`{"kind":"basic","fields":{"front":"q1","back":"a1"},"external_ref":"n:1"},` +
		`{"kind":"basic","fields":{"front":"q2","back":"a2"},"external_ref":"n:2"}]}`
	if st, raw := rest(t, base, http.MethodPost, deckPath, key, body); st != 200 {
		t.Fatalf("seed import status = %d body %v", st, raw)
	}

	// list_decks
	mcpOut, _, _ := callTool(t, cs, "list_decks", map[string]any{})
	if _, restOut := rest(t, base, http.MethodGet, "/api/v1/decks", key, ""); !reflect.DeepEqual(mcpOut, restOut) {
		t.Errorf("list_decks MCP=%v REST=%v", mcpOut, restOut)
	}

	// search_notes（显式分页，两侧一致）
	mcpOut, _, _ = callTool(t, cs, "search_notes", map[string]any{"deck_id": deck.ID, "page": 1, "per_page": 20})
	if _, restOut := rest(t, base, http.MethodGet, fmt.Sprintf("/api/v1/decks/%d/notes?page=1&per_page=20", deck.ID), key, ""); !reflect.DeepEqual(mcpOut, restOut) {
		t.Errorf("search_notes MCP=%v REST=%v", mcpOut, restOut)
	}

	// get_stats
	mcpOut, _, _ = callTool(t, cs, "get_stats", map[string]any{})
	if _, restOut := rest(t, base, http.MethodGet, "/api/v1/stats/summary", key, ""); !reflect.DeepEqual(mcpOut, restOut) {
		t.Errorf("get_stats MCP=%v REST=%v", mcpOut, restOut)
	}

	// export_deck（M5-8）：返回卡组包文档，含 manifest/notes/cards/preset。
	mcpOut, isErr, text := callTool(t, cs, "export_deck", map[string]any{"deck_id": deck.ID})
	if isErr {
		t.Fatalf("export_deck error: %s", text)
	}
	manifest, _ := mcpOut["manifest.json"].(map[string]any)
	if manifest == nil {
		t.Fatalf("export_deck did not return a package document: %v", mcpOut)
	}
	counts, _ := manifest["counts"].(map[string]any)
	if got := counts["notes"]; got != float64(2) {
		t.Errorf("package manifest counts.notes = %v, want 2", got)
	}
	if _, ok := mcpOut["notes.json"]; !ok {
		t.Errorf("package document is missing notes.json: %v", mcpOut)
	}

	// create_notes / import_deck（dry_run，同一 deck 状态下产出相同计数）
	dry := map[string]any{"deck_id": deck.ID, "dry_run": true, "notes": []any{
		map[string]any{"kind": "basic", "fields": map[string]any{"front": "x", "back": "y"}, "external_ref": "n:3"},
	}}
	mcpOut, _, _ = callTool(t, cs, "create_notes", dry)
	if _, restOut := rest(t, base, http.MethodPost, deckPath, key, `{"dry_run":true,"notes":[{"kind":"basic","fields":{"front":"x","back":"y"},"external_ref":"n:3"}]}`); !reflect.DeepEqual(mcpOut, restOut) {
		t.Errorf("create_notes(dry_run) MCP=%v REST=%v", mcpOut, restOut)
	}

	// import_deck（M5-8）：接受 export_deck 输出的文档，dry_run 预演不写入。
	pkgDoc, _, _ := callTool(t, cs, "export_deck", map[string]any{"deck_id": deck.ID, "include_progress": true})
	mcpOut, isErr, text = callTool(t, cs, "import_deck", map[string]any{"package": pkgDoc, "dry_run": true, "target": "new_deck"})
	if isErr {
		t.Fatalf("import_deck error: %s", text)
	}
	if mcpOut["dry_run"] != true || mcpOut["notes_created"] != float64(2) {
		t.Errorf("import_deck(dry_run) report = %v, want dry_run=true notes_created=2", mcpOut)
	}
	if _, ok := mcpOut["errors"]; !ok {
		t.Errorf("import_deck report is missing the errors field: %v", mcpOut)
	}

	// 取得 note id 以便 update/delete。
	_, notesOut := rest(t, base, http.MethodGet, fmt.Sprintf("/api/v1/decks/%d/notes?per_page=50", deck.ID), key, "")
	byRef := map[string]float64{}
	for _, raw := range notesOut["notes"].([]any) {
		n := raw.(map[string]any)
		byRef[fmt.Sprint(n["external_ref"])] = n["id"].(float64)
	}
	note1, note2 := uint64(byRef["n:1"]), uint64(byRef["n:2"])

	// update_note：内容相同，去掉动态字段后应一致。
	mcpUpd, _, _ := callTool(t, cs, "update_note", map[string]any{"note_id": note1, "kind": "basic", "fields": map[string]any{"front": "u", "back": "Y"}, "tags": []any{"t1"}})
	_, restUpd := rest(t, base, http.MethodPatch, fmt.Sprintf("/api/v1/notes/%d", note2), key, `{"kind":"basic","fields":{"front":"u","back":"Y"},"tags":["t1"]}`)
	if !reflect.DeepEqual(stripDynamic(mcpUpd), stripDynamic(restUpd)) {
		t.Errorf("update_note MCP=%v REST=%v", stripDynamic(mcpUpd), stripDynamic(restUpd))
	}

	// get_due_cards（此时 note 尚未删除，队列两侧应一致；due_at 依赖调用时刻，比较前归一化）
	mcpOut, _, _ = callTool(t, cs, "get_due_cards", map[string]any{"deck_id": deck.ID, "limit": 50})
	if _, restOut := rest(t, base, http.MethodGet, fmt.Sprintf("/api/v1/review/due?deck=%d&limit=50", deck.ID), key, ""); !reflect.DeepEqual(normalizeCards(mcpOut), normalizeCards(restOut)) {
		t.Errorf("get_due_cards MCP=%v REST=%v", normalizeCards(mcpOut), normalizeCards(restOut))
	}

	// submit_review：两张卡内容一致，去掉 id 维度后结果应一致。
	_, exp := rest(t, base, http.MethodGet, fmt.Sprintf("/api/v1/export?deck=%d&format=json", deck.ID), key, "")
	cards := exp["cards"].([]any)
	if len(cards) < 2 {
		t.Fatalf("expected >=2 cards for review, got %d", len(cards))
	}
	var ids []float64
	for _, raw := range cards {
		ids = append(ids, raw.(map[string]any)["card_id"].(float64))
	}
	mcpRev, isErr, text := callTool(t, cs, "submit_review", map[string]any{"card_id": uint64(ids[0]), "rating": 3, "expected_version": 0})
	if isErr {
		t.Fatalf("submit_review MCP error: %s", text)
	}
	_, restRev := rest(t, base, http.MethodPost, "/api/v1/review", key, fmt.Sprintf(`{"card_id":%d,"rating":3,"expected_version":0}`, uint64(ids[1])))
	if !reflect.DeepEqual(stripReviewIDs(mcpRev), stripReviewIDs(restRev)) {
		t.Errorf("submit_review MCP=%v REST=%v", stripReviewIDs(mcpRev), stripReviewIDs(restRev))
	}

	// delete_note（放在最后，避免影响上面的导出与复习队列）
	mcpDel, _, _ := callTool(t, cs, "delete_note", map[string]any{"note_id": note1})
	_, restDel := rest(t, base, http.MethodDelete, fmt.Sprintf("/api/v1/notes/%d", note2), key, "")
	if !reflect.DeepEqual(stripDynamic(mcpDel), stripDynamic(restDel)) {
		t.Errorf("delete_note MCP=%v REST=%v", mcpDel, restDel)
	}
}

// stripDynamic 去掉随写入变化的字段，便于比较两侧同内容对象。
func stripDynamic(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		switch k {
		case "id", "created_at", "updated_at", "external_ref":
			continue
		}
		out[k] = v
	}
	return out
}

// stripReviewIDs 去掉评测结果里与具体卡和调用时刻相关的字段。
func stripReviewIDs(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		switch k {
		case "card_id", "review_id", "due_at":
			continue
		}
		out[k] = v
	}
	return out
}

// normalizeCards 去掉到期卡里依赖调用时刻的 due_at，并按 card_id 排序，便于两侧比较。
func normalizeCards(m map[string]any) map[string]any {
	cards, _ := m["cards"].([]any)
	for _, raw := range cards {
		if c, ok := raw.(map[string]any); ok {
			delete(c, "due_at")
		}
	}
	sort.Slice(cards, func(i, j int) bool {
		return cards[i].(map[string]any)["card_id"].(float64) < cards[j].(map[string]any)["card_id"].(float64)
	})
	m["cards"] = cards
	return m
}
