package mcp

import (
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
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

// ---- M3-13：多卡组复习范围 ----

// seedDueCardMCP 建一个 note 与它的一张 card，不写 card_states：该卡是新卡，必然出现在队列里。
func seedDueCardMCP(t *testing.T, db *gorm.DB, deckID uint64) {
	t.Helper()
	now := time.Now().UTC()
	n := store.Note{DeckID: deckID, Kind: "basic", FieldsJSON: `{"front":"q","back":"a"}`, TagsJSON: "[]", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&n).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	c := store.Card{NoteID: n.ID, Template: "forward", Ordinal: 0, CreatedAt: now}
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
}

// TestGetDueCardsDeckIDsMatchesREST 断言 get_due_cards 的 deck_ids 与 REST 的重复 deck 参数
// 是同一口径；deck_id 与 deck_ids 同时给出返回工具错误（isErr）。
func TestGetDueCardsDeckIDsMatchesREST(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "multideck")
	deckA := seedDeck(t, db, u.ID)
	deckB := seedDeck(t, db, u.ID)
	seedDueCardMCP(t, db, deckA.ID)
	seedDueCardMCP(t, db, deckB.ID)
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeReview})
	cs := connect(t, ts.URL, key)

	mcpOut, isErr, text := callTool(t, cs, "get_due_cards", map[string]any{
		"deck_ids": []any{deckA.ID, deckB.ID}, "limit": 50,
	})
	if isErr {
		t.Fatalf("get_due_cards(deck_ids) error: %s", text)
	}
	_, restOut := rest(t, ts.URL, http.MethodGet,
		fmt.Sprintf("/api/v1/review/due?deck=%d&deck=%d&limit=50", deckA.ID, deckB.ID), key, "")
	if !reflect.DeepEqual(normalizeCards(mcpOut), normalizeCards(restOut)) {
		t.Errorf("get_due_cards(deck_ids) MCP=%v REST=%v", normalizeCards(mcpOut), normalizeCards(restOut))
	}

	// 互斥：同时给出 deck_id 与 deck_ids 是参数错误，客户端必须看到 isErr。
	_, isErr, text = callTool(t, cs, "get_due_cards", map[string]any{
		"deck_id": deckA.ID, "deck_ids": []any{deckB.ID},
	})
	if !isErr {
		t.Fatalf("get_due_cards with both deck_id and deck_ids must fail")
	}
	if !strings.Contains(text, "invalid_request") {
		t.Errorf("mutual-exclusion error text = %q, want it to carry code invalid_request", text)
	}
}

// ---- M4-11：create_deck 与 REST 建卡组同源 ----

// errorCode 取 REST 统一错误包壳里的稳定 code。
func errorCode(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

// TestCreateDeckMatchesREST 是 M4-11 的核心验收：create_deck 与 REST POST /decks 同参数同结果，
// 空名字/非法 visibility 用共享 code 拒绝，preset_id=0 落在调用者的 Default 预设，且无 write
// scope 的 key 按名字硬调也被拒。
func TestCreateDeckMatchesREST(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "deckmaker")
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite})
	cs := connect(t, ts.URL, key)

	// 同一参数：MCP 建一个、REST 建一个，去掉动态字段后应完全一致。
	mcpOut, isErr, text := callTool(t, cs, "create_deck", map[string]any{
		"name": "  Parity  ", "description": "same args", "visibility": "unlisted",
	})
	if isErr {
		t.Fatalf("create_deck error: %s", text)
	}
	if mcpOut["name"] != "Parity" {
		t.Errorf("create_deck name = %v, want trimmed %q", mcpOut["name"], "Parity")
	}
	st, restOut := rest(t, ts.URL, http.MethodPost, "/api/v1/decks", key,
		`{"name":"Parity","description":"same args","visibility":"unlisted"}`)
	if st != http.StatusCreated {
		t.Fatalf("REST create status = %d body %v", st, restOut)
	}
	if !reflect.DeepEqual(stripDynamic(mcpOut), stripDynamic(restOut)) {
		t.Errorf("create_deck MCP=%v REST=%v", stripDynamic(mcpOut), stripDynamic(restOut))
	}

	// preset_id 为 0 时落在调用者的 Default 预设上（两侧都非 0 且相同）。
	pid, ok := mcpOut["preset_id"].(float64)
	if !ok || pid == 0 {
		t.Fatalf("create_deck preset_id = %v, want the caller's Default preset id", mcpOut["preset_id"])
	}
	var defaultPreset store.Preset
	if err := db.Where("owner_user_id = ? AND name = ?", u.ID, "Default").First(&defaultPreset).Error; err != nil {
		t.Fatalf("load Default preset: %v", err)
	}
	if uint64(pid) != defaultPreset.ID {
		t.Errorf("create_deck preset_id = %d, want Default preset %d", uint64(pid), defaultPreset.ID)
	}

	// 空名字：MCP 与 REST 都用共享的 invalid_request code 拒绝。
	_, isErr, text = callTool(t, cs, "create_deck", map[string]any{"name": "   "})
	if !isErr || !strings.Contains(text, api.CodeInvalidRequest) {
		t.Errorf("create_deck(blank name) isErr=%v text=%q, want %s", isErr, text, api.CodeInvalidRequest)
	}
	st, restOut = rest(t, ts.URL, http.MethodPost, "/api/v1/decks", key, `{"name":"   "}`)
	if st != http.StatusBadRequest || errorCode(restOut) != api.CodeInvalidRequest {
		t.Errorf("REST blank name status=%d code=%q, want 400 %s", st, errorCode(restOut), api.CodeInvalidRequest)
	}

	// 非法 visibility：同样由共享 code 拒绝。
	_, isErr, text = callTool(t, cs, "create_deck", map[string]any{"name": "x", "visibility": "bogus"})
	if !isErr || !strings.Contains(text, api.CodeInvalidRequest) {
		t.Errorf("create_deck(bad visibility) isErr=%v text=%q, want %s", isErr, text, api.CodeInvalidRequest)
	}
	st, restOut = rest(t, ts.URL, http.MethodPost, "/api/v1/decks", key, `{"name":"x","visibility":"bogus"}`)
	if st != http.StatusBadRequest || errorCode(restOut) != api.CodeInvalidRequest {
		t.Errorf("REST bad visibility status=%d code=%q, want 400 %s", st, errorCode(restOut), api.CodeInvalidRequest)
	}

	// 无 write scope 的 key 即使按名字硬调 create_deck 也被拒。
	roCS := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeRead}))
	_, isErr, text = callTool(t, roCS, "create_deck", map[string]any{"name": "nope"})
	if !isErr || !strings.Contains(text, api.CodeScopeRequired) {
		t.Errorf("create_deck with read-only key isErr=%v text=%q, want %s", isErr, text, api.CodeScopeRequired)
	}
}
