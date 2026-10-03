package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// seedBasicNote 用 NoteStore.Create 建一条 basic note（同时生成它的 cards），返回 note。
// 夹具走真实的生成管线，保证批量动作测试面对的是真实落库的 note 与 card。
func seedBasicNote(t *testing.T, env *testEnv, deckID uint64, front, back string, tags []string) *store.Note {
	t.Helper()
	n := &store.Note{DeckID: deckID, Kind: "basic", TagsJSON: tagsJSON(tags), CreatedAt: time.Now().UTC()}
	if _, err := store.NewNoteStore(env.db).Create(context.Background(), n, map[string]any{"front": front, "back": back}); err != nil {
		t.Fatalf("create note: %v", err)
	}
	return n
}

// bulkResponse 解析批量动作响应体，便于逐字段断言。
type bulkResponse struct {
	DryRun   bool  `json:"dry_run"`
	Affected int64 `json:"affected"`
	Skipped  []struct {
		NoteID uint64 `json:"note_id"`
		Code   string `json:"code"`
	} `json:"skipped"`
}

func decodeBulk(t *testing.T, raw []byte) bulkResponse {
	t.Helper()
	var r bulkResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("bulk response is not JSON: %v (%s)", err, raw)
	}
	return r
}

// assertErrorCode 断言错误包壳里的稳定 code。
func assertErrorCode(t *testing.T, raw []byte, want string) {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("error response is not JSON: %v (%s)", err, raw)
	}
	if body.Error.Code != want {
		t.Errorf("error code = %q, want %q (body %s)", body.Error.Code, want, raw)
	}
}

// TestBulkNotesRejectsInvalidRequestShapes 覆盖四类（此处五条）请求级拒绝：
// 未知 action、note_ids 为空、note_ids 超过 500、tag 动作缺 tags、delete 带 tags。
// 每条都必须 400 invalid_request，且一行都不写、不写审计。
func TestBulkNotesRejectsInvalidRequestShapes(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "bulk_reject", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	note := seedBasicNote(t, env, deck.ID, "q", "a", nil)
	ctx := context.Background()

	var b strings.Builder
	b.WriteString("[")
	for i := 1; i <= 501; i++ {
		if i > 1 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "%d", i)
	}
	b.WriteString("]")

	cases := []struct{ name, body string }{
		{"unknown action", fmt.Sprintf(`{"action":"archive","note_ids":[%d]}`, note.ID)},
		{"empty note_ids", `{"action":"delete","note_ids":[]}`},
		{"oversized note_ids", `{"action":"delete","note_ids":` + b.String() + `}`},
		{"tag action without tags", fmt.Sprintf(`{"action":"add_tags","note_ids":[%d]}`, note.ID)},
		{"delete with tags", fmt.Sprintf(`{"action":"delete","note_ids":[%d],"tags":["x"]}`, note.ID)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, raw := doJSON(t, router, http.MethodPost, "/api/v1/notes/bulk", k.Plaintext, tc.body)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", status, raw)
			}
			assertErrorCode(t, raw, CodeInvalidRequest)
		})
	}

	// 零写入：note 未删、未打标签，且没有任何审计行。
	var notes int64
	if err := env.db.Model(&store.Note{}).Where("deck_id = ?", deck.ID).Count(&notes).Error; err != nil {
		t.Fatalf("count notes: %v", err)
	}
	if notes != 1 {
		t.Errorf("notes in deck = %d, want 1 (rejected requests must write nothing)", notes)
	}
	if n, err := store.NewAuditStore(env.db).CountByAction(ctx, store.ActionNoteDelete); err != nil || n != 0 {
		t.Errorf("note.delete audit rows = (%d, %v), want (0, nil)", n, err)
	}
	// 每个批量动作都不得留下审计行（认证中间件会为请求本身写一行，故不整表计数）。
	audit := store.NewAuditStore(env.db)
	for _, action := range []string{store.ActionNoteDelete, store.ActionNoteTagAdd, store.ActionNoteTagRemove, store.ActionNoteTagSet} {
		if n, err := audit.CountByAction(ctx, action); err != nil || n != 0 {
			t.Errorf("audit rows for %q = (%d, %v), want (0, nil)", action, n, err)
		}
	}
}

// TestBulkNotesSkipsMissingDeletedAndUnreadableRows 覆盖逐行判权：
// 缺失与软删的 id -> not_found，无权编辑的 id -> insufficient_role，
// 同一请求里其余被允许的行照常生效（单行被拒不回滚整批）。
func TestBulkNotesSkipsMissingDeletedAndUnreadableRows(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	ctx := context.Background()
	owner := seedUser(t, env.db, "bulk_owner", store.RoleUser)
	other := seedUser(t, env.db, "bulk_other", store.RoleUser)
	deckA := seedDeck(t, env.db, owner.ID) // owner 是属主，可编辑
	deckB := seedDeck(t, env.db, other.ID) // owner 仅被授予 reader
	deckC := seedDeck(t, env.db, other.ID) // owner 完全无权

	editable := seedBasicNote(t, env, deckA.ID, "q1", "a1", nil)
	deleted := seedBasicNote(t, env, deckA.ID, "q2", "a2", nil)
	readable := seedBasicNote(t, env, deckB.ID, "q3", "a3", nil)
	hidden := seedBasicNote(t, env, deckC.ID, "q4", "a4", nil)

	if err := store.NewGrantStore(env.db).Grant(ctx, deckB.ID, owner.ID, store.RoleReader, store.Ptr(other.ID)); err != nil {
		t.Fatalf("grant reader: %v", err)
	}
	if err := store.NewNoteStore(env.db).Delete(ctx, deleted.ID); err != nil {
		t.Fatalf("soft-delete note: %v", err)
	}

	k := seedKey(t, env.keys, owner.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	body := fmt.Sprintf(`{"action":"add_tags","note_ids":[%d,%d,%d,%d,999999],"tags":["bulk"]}`,
		editable.ID, deleted.ID, readable.ID, hidden.ID)

	status, raw := doJSON(t, router, http.MethodPost, "/api/v1/notes/bulk", k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	resp := decodeBulk(t, raw)
	if resp.Affected != 1 {
		t.Errorf("affected = %d, want 1 (only the editable row changes)", resp.Affected)
	}
	want := []struct {
		id   uint64
		code string
	}{
		{deleted.ID, "not_found"},
		{readable.ID, "insufficient_role"},
		{hidden.ID, "insufficient_role"},
		{999999, "not_found"},
	}
	if len(resp.Skipped) != len(want) {
		t.Fatalf("skipped = %+v, want %d entries", resp.Skipped, len(want))
	}
	for i, w := range want {
		if resp.Skipped[i].NoteID != w.id || resp.Skipped[i].Code != w.code {
			t.Errorf("skipped[%d] = (%d,%q), want (%d,%q)", i, resp.Skipped[i].NoteID, resp.Skipped[i].Code, w.id, w.code)
		}
	}

	// 只有被允许的那一行真的改了；其余三行原样保留。
	noteStore := store.NewNoteStore(env.db)
	tags, _ := noteStore.ByID(ctx, editable.ID)
	if got, _ := store.ParseTags(tags.TagsJSON); !containsTag(got, "bulk") {
		t.Errorf("editable note tags = %v, want to contain bulk", got)
	}
	for _, id := range []uint64{readable.ID, hidden.ID} {
		n, err := noteStore.ByID(ctx, id)
		if err != nil {
			t.Fatalf("reload note %d: %v", id, err)
		}
		if got, _ := store.ParseTags(n.TagsJSON); len(got) != 0 {
			t.Errorf("note %d tags = %v, want unchanged (empty)", id, got)
		}
	}
}

// TestBulkNotesIsIdempotentAndAuditsOnce 断言重复提交同一请求第二次 affected=0，
// 且整批只写一行审计，detail 里带 ids 与 affected。
func TestBulkNotesIsIdempotentAndAuditsOnce(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "bulk_idem", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	note := seedBasicNote(t, env, deck.ID, "q", "a", nil)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	body := fmt.Sprintf(`{"action":"add_tags","note_ids":[%d],"tags":["alpha","beta"]}`, note.ID)

	status, raw := doJSON(t, router, http.MethodPost, "/api/v1/notes/bulk", k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("first status = %d, want 200 (body %s)", status, raw)
	}
	if first := decodeBulk(t, raw); first.Affected != 1 {
		t.Fatalf("first affected = %d, want 1", first.Affected)
	}

	status, raw = doJSON(t, router, http.MethodPost, "/api/v1/notes/bulk", k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("second status = %d, want 200 (body %s)", status, raw)
	}
	if second := decodeBulk(t, raw); second.Affected != 0 {
		t.Errorf("second affected = %d, want 0 (repeating the request is idempotent)", second.Affected)
	}

	audit := store.NewAuditStore(env.db)
	if n, err := audit.CountByAction(context.Background(), store.ActionNoteTagAdd); err != nil || n != 2 {
		// 两次请求各写一行审计（整批一行）；affected 第二次为 0 仍留痕。
		t.Errorf("note.tag_add audit rows = (%d, %v), want (2, nil)", n, err)
	}
	var last store.AuditLog
	if err := env.db.Where("action = ?", store.ActionNoteTagAdd).Order("id DESC").First(&last).Error; err != nil {
		t.Fatalf("load latest audit: %v", err)
	}
	if last.DetailJSON == nil {
		t.Fatal("bulk audit detail_json is nil, want ids and affected")
	}
	for _, want := range []string{`"ids":[`, fmt.Sprintf(`"affected":%d`, 0)} {
		if !strings.Contains(*last.DetailJSON, want) {
			t.Errorf("audit detail %s does not contain %s", *last.DetailJSON, want)
		}
	}
}

// TestBulkNotesDryRunChangesNothing 断言 dry_run 只计数：不改任何行、不写审计。
func TestBulkNotesDryRunChangesNothing(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "bulk_dry", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	a := seedBasicNote(t, env, deck.ID, "q1", "a1", nil)
	b := seedBasicNote(t, env, deck.ID, "q2", "a2", nil)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	router := env.router()

	body := fmt.Sprintf(`{"action":"add_tags","dry_run":true,"note_ids":[%d,%d],"tags":["gamma"]}`, a.ID, b.ID)
	status, raw := doJSON(t, router, http.MethodPost, "/api/v1/notes/bulk", k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	resp := decodeBulk(t, raw)
	if !resp.DryRun || resp.Affected != 2 {
		t.Errorf("dry_run response = (dry_run=%v affected=%d), want (true, 2)", resp.DryRun, resp.Affected)
	}

	noteStore := store.NewNoteStore(env.db)
	for _, id := range []uint64{a.ID, b.ID} {
		n, err := noteStore.ByID(context.Background(), id)
		if err != nil {
			t.Fatalf("reload note %d: %v", id, err)
		}
		if got, _ := store.ParseTags(n.TagsJSON); len(got) != 0 {
			t.Errorf("note %d tags = %v, want unchanged (dry_run must not write)", id, got)
		}
	}
	// dry_run 不写任何批量动作审计行（认证中间件为请求本身写的那一行不算）。
	audit := store.NewAuditStore(env.db)
	for _, action := range []string{store.ActionNoteDelete, store.ActionNoteTagAdd, store.ActionNoteTagRemove, store.ActionNoteTagSet} {
		if n, err := audit.CountByAction(context.Background(), action); err != nil || n != 0 {
			t.Errorf("dry_run audit rows for %q = (%d, %v), want (0, nil)", action, n, err)
		}
	}
}
