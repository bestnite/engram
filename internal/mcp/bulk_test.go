package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
	gorm "gorm.io/gorm"
)

// ---- M4-13：bulk_notes 工具与 create_notes 按 note_id 寻址 ----

// toMap 把 service 层的响应体转成 JSON map，便于与 MCP structured content 逐字段比较。
func toMap(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return m
}

// noteIDsInDeck 按 id 升序取出卡组内的 note id，供两侧各取一张做对照。
func noteIDsInDeck(t *testing.T, db *gorm.DB, deckID uint64) []uint64 {
	t.Helper()
	var notes []store.Note
	if err := db.Where("deck_id = ?", deckID).Order("id").Find(&notes).Error; err != nil {
		t.Fatalf("list notes: %v", err)
	}
	ids := make([]uint64, 0, len(notes))
	for _, n := range notes {
		ids = append(ids, n.ID)
	}
	return ids
}

// TestBulkNotesMatchesREST 是 M4-13 的核心验收：bulk_notes 与直接调用 api.BulkNotes（REST
// POST /notes/bulk 的 service 方法）在同一输入下产出同一返回体，并真正改写库中的 tags_json；
// 重复提交同一请求第二次 affected=0（幂等）。
func TestBulkNotesMatchesREST(t *testing.T) {
	apiSrv, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "bulkuser")
	deck := seedDeck(t, db, u.ID)
	// 两张内容相同的 note：一张走 MCP，一张走直接 service 调用，比较两侧结果。
	seedDueCardMCP(t, db, deck.ID)
	seedDueCardMCP(t, db, deck.ID)
	ids := noteIDsInDeck(t, db, deck.ID)
	if len(ids) != 2 {
		t.Fatalf("seeded notes = %d, want 2", len(ids))
	}
	n1, n2 := ids[0], ids[1]

	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeWrite}))

	// 第一次 add_tags：两侧应各改动一行。
	mcpOut, isErr, text := callTool(t, cs, "bulk_notes", map[string]any{
		"action": "add_tags", "note_ids": []any{n1}, "tags": []any{"t1"},
	})
	if isErr {
		t.Fatalf("bulk_notes(add_tags) error: %s", text)
	}
	direct, err := apiSrv.BulkNotes(context.Background(), u.ID, nil, api.BulkNotesInput{
		Action: "add_tags", NoteIDs: []uint64{n2}, Tags: []string{"t1"},
	})
	if err != nil {
		t.Fatalf("direct BulkNotes: %v", err)
	}
	if !reflect.DeepEqual(mcpOut, toMap(t, direct)) {
		t.Errorf("bulk_notes(add_tags) MCP=%v direct=%v", mcpOut, toMap(t, direct))
	}
	if mcpOut["affected"] != float64(1) {
		t.Errorf("bulk_notes(add_tags) affected = %v, want 1", mcpOut["affected"])
	}

	// 回读库里的 tags_json：两条 note 都应当带上 t1。
	noteStore := store.NewNoteStore(db)
	for _, id := range ids {
		n, err := noteStore.ByID(context.Background(), id)
		if err != nil {
			t.Fatalf("reload note %d: %v", id, err)
		}
		if tags, _ := store.ParseTags(n.TagsJSON); !containsStr(tags, "t1") {
			t.Errorf("note %d tags = %v, want to contain t1", id, tags)
		}
	}

	// 重复提交同一请求：两侧 affected 都归零（幂等）。
	mcpOut, _, _ = callTool(t, cs, "bulk_notes", map[string]any{
		"action": "add_tags", "note_ids": []any{n1}, "tags": []any{"t1"},
	})
	direct, err = apiSrv.BulkNotes(context.Background(), u.ID, nil, api.BulkNotesInput{
		Action: "add_tags", NoteIDs: []uint64{n2}, Tags: []string{"t1"},
	})
	if err != nil {
		t.Fatalf("direct BulkNotes (repeat): %v", err)
	}
	if !reflect.DeepEqual(mcpOut, toMap(t, direct)) {
		t.Errorf("bulk_notes(repeat) MCP=%v direct=%v", mcpOut, toMap(t, direct))
	}
	if mcpOut["affected"] != float64(0) {
		t.Errorf("bulk_notes(repeat) affected = %v, want 0", mcpOut["affected"])
	}
}

// containsStr 判断字符串切片是否含目标值（本包测试辅助）。
func containsStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// TestBulkNotesHiddenFromReadKey 断言只读 key 在 tools/list 里看不到 bulk_notes，
// 且按名字硬调返回 scope_required（M4-6 的复查逻辑覆盖新工具）。
func TestBulkNotesHiddenFromReadKey(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "readonlybulk")
	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeRead}))

	for _, name := range toolNames(t, cs) {
		if name == "bulk_notes" {
			t.Fatalf("read-only key must not see bulk_notes in tools/list")
		}
	}
	_, isErr, text := callTool(t, cs, "bulk_notes", map[string]any{"action": "delete", "note_ids": []any{1}})
	if !isErr || !strings.Contains(text, api.CodeScopeRequired) {
		t.Fatalf("hard-calling bulk_notes with a read-only key: isErr=%v text=%q, want %s", isErr, text, api.CodeScopeRequired)
	}
}

// TestCreateNotesByNoteIDRewritesInPlace 覆盖 M4-13 第二部分：create_notes 的 item 带 note_id
// 时原地改写已有 note 的字段，保留 card id 与复习进度；note_id 与 external_ref 同时给出时该行报 errors[]。
func TestCreateNotesByNoteIDRewritesInPlace(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "byid")
	deck := seedDeck(t, db, u.ID)
	seedDueCardMCP(t, db, deck.ID)

	var note store.Note
	if err := db.Where("deck_id = ?", deck.ID).First(&note).Error; err != nil {
		t.Fatalf("load seeded note: %v", err)
	}
	var card store.Card
	if err := db.Where("note_id = ?", note.ID).First(&card).Error; err != nil {
		t.Fatalf("load seeded card: %v", err)
	}
	due := time.Now().UTC()
	if err := db.Create(&store.CardState{
		CardID: card.ID, UserID: u.ID, State: "review", DueAt: &due, Reps: 4, Lapses: 2,
	}).Error; err != nil {
		t.Fatalf("create card state: %v", err)
	}

	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeWrite}))

	out, isErr, text := callTool(t, cs, "create_notes", map[string]any{
		"deck_id": deck.ID,
		"notes": []any{map[string]any{
			"note_id": note.ID, "kind": "basic",
			"fields": map[string]any{"front": "new", "back": "newA"}, "tags": []any{"t1"},
		}},
	})
	if isErr {
		t.Fatalf("create_notes(note_id) error: %s", text)
	}
	if out["updated"] != float64(1) || out["created"] != float64(0) {
		t.Errorf("create_notes(note_id) updated/created = %v/%v, want 1/0", out["updated"], out["created"])
	}

	// 字段被就地改写。
	updated, err := store.NewNoteStore(db).ByID(context.Background(), note.ID)
	if err != nil {
		t.Fatalf("reload note: %v", err)
	}
	if fields, _ := store.ParseFields(updated.FieldsJSON); fields["front"] != "new" || fields["back"] != "newA" {
		t.Errorf("note fields = %v, want the imported values", fields)
	}

	// card id 与复习进度必须原样保留。
	var cards []store.Card
	if err := db.Where("note_id = ?", note.ID).Find(&cards).Error; err != nil {
		t.Fatalf("reload cards: %v", err)
	}
	if len(cards) != 1 || cards[0].ID != card.ID {
		t.Fatalf("cards after in-place rewrite = %+v, want the original card %d", cards, card.ID)
	}
	var state store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", card.ID, u.ID).First(&state).Error; err != nil {
		t.Fatalf("reload card state: %v", err)
	}
	if state.Reps != 4 || state.Lapses != 2 {
		t.Errorf("card state = (reps %d, lapses %d), want (4, 2)", state.Reps, state.Lapses)
	}

	// note_id 与 external_ref 同时给出：该行报一条行错误，其余不变。
	out, isErr, text = callTool(t, cs, "create_notes", map[string]any{
		"deck_id": deck.ID,
		"notes": []any{map[string]any{
			"note_id": note.ID, "kind": "basic",
			"fields": map[string]any{"front": "z", "back": "z"}, "external_ref": "e:1",
		}},
	})
	if isErr {
		t.Fatalf("create_notes(note_id+external_ref) transport error: %s", text)
	}
	errs, _ := out["errors"].([]any)
	if len(errs) != 1 {
		t.Fatalf("create_notes(note_id+external_ref) errors = %v, want exactly one row error", out["errors"])
	}
	if out["updated"] != float64(0) || out["created"] != float64(0) {
		t.Errorf("create_notes(note_id+external_ref) updated/created = %v/%v, want 0/0", out["updated"], out["created"])
	}
	reloaded, err := store.NewNoteStore(db).ByID(context.Background(), note.ID)
	if err != nil {
		t.Fatalf("reload note after rejected row: %v", err)
	}
	if fields, _ := store.ParseFields(reloaded.FieldsJSON); fields["front"] != "new" {
		t.Errorf("rejected row must not change the note: fields = %v, want front=new", fields)
	}
}
