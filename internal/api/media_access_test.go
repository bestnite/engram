package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件覆盖 REST 入口的 L2 越权注入：共享卡组的 editor 把别人媒体的引用注入到自己可编辑的
// note 里，写前校验必须拒绝（稳定 code media_not_readable），且库里没有留下脏数据。

// seedMedia 落盘一份媒体并返回 sha256（登记上传者归属）。
func seedMedia(t *testing.T, env *testEnv, ownerID uint64, payload string) string {
	t.Helper()
	raw := append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, []byte(payload)...)
	m, err := store.NewMediaStore(env.db).SaveBytes(context.Background(), t.TempDir(), "image/png", raw, store.Ptr(ownerID))
	if err != nil {
		t.Fatalf("SaveBytes: %v", err)
	}
	return m.Sha256
}

// seedPlainNote 经写入方法建一条不含媒体引用的 note。
func seedPlainNote(t *testing.T, env *testEnv, deckID, authorID uint64) *store.Note {
	t.Helper()
	n := store.Note{DeckID: deckID, Kind: "basic", CreatedBy: store.Ptr(authorID)}
	if _, err := store.NewNoteStore(env.db).Save(context.Background(), &n, map[string]any{"front": "plain", "back": "x"}); err != nil {
		t.Fatalf("save note: %v", err)
	}
	return &n
}

// TestRESTUpdateNoteRejectsUnreadableMediaInjection 覆盖 REST `PATCH /notes/:id` 的写前校验。
func TestRESTUpdateNoteRejectsUnreadableMediaInjection(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "restmed_owner", store.RoleUser)
	attacker := seedUser(t, env.db, "restmed_editor", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	if err := store.NewGrantStore(env.db).Grant(context.Background(), deck.ID, attacker.ID, store.RoleEditor, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant editor: %v", err)
	}
	sha := seedMedia(t, env, owner.ID, "rest-injection")
	note := seedPlainNote(t, env, deck.ID, owner.ID)

	k := seedKey(t, env.keys, attacker.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	body := fmt.Sprintf(`{"fields":{"front":"![](/media/%s)","back":"x"}}`, sha)

	status, raw := doJSON(t, router, http.MethodPatch, fmt.Sprintf("/api/v1/notes/%s", note.PublicID), k.Plaintext, body)
	if status != http.StatusForbidden {
		t.Fatalf("PATCH injection status = %d, want 403 (body %s)", status, raw)
	}
	var envl struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &envl); err != nil {
		t.Fatalf("decode error body: %v (%s)", err, raw)
	}
	if envl.Error.Code != CodeMediaNotReadable {
		t.Errorf("error code = %q, want %q (body %s)", envl.Error.Code, CodeMediaNotReadable, raw)
	}

	reloaded, err := store.NewNoteStore(env.db).ByID(context.Background(), note.ID)
	if err != nil {
		t.Fatalf("reload note: %v", err)
	}
	if reloaded.FieldsJSON != note.FieldsJSON {
		t.Errorf("rejected update changed the note fields: got %s want %s", reloaded.FieldsJSON, note.FieldsJSON)
	}
	var mapping int64
	if err := env.db.Model(&store.MediaNote{}).Where("media_sha = ? AND note_id = ?", sha, note.ID).Count(&mapping).Error; err != nil {
		t.Fatalf("count media_notes: %v", err)
	}
	if mapping != 0 {
		t.Errorf("rejected update left %d media_notes rows, want 0", mapping)
	}
}

// TestRESTBulkImportRejectsUnreadableMediaInjection 覆盖 REST `POST /decks/:id/notes`（批量导入）
// 的写前校验：越权引用那一行报稳定 code，且不落库。
func TestRESTBulkImportRejectsUnreadableMediaInjection(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "restbulk_owner", store.RoleUser)
	attacker := seedUser(t, env.db, "restbulk_editor", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	if err := store.NewGrantStore(env.db).Grant(context.Background(), deck.ID, attacker.ID, store.RoleEditor, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant editor: %v", err)
	}
	sha := seedMedia(t, env, owner.ID, "rest-bulk")

	k := seedKey(t, env.keys, attacker.ID, []string{store.ScopeWrite, store.ScopeRead}, nil)
	router := env.router()
	body := fmt.Sprintf(`{"notes":[{"kind":"basic","fields":{"front":"![](/media/%s)","back":"x"}}]}`, sha)

	status, raw := doJSON(t, router, http.MethodPost, fmt.Sprintf("/api/v1/decks/%s/notes", deck.PublicID), k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("bulk import status = %d, want 200 with per-row error (body %s)", status, raw)
	}
	var resp ImportResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode import response: %v (%s)", err, raw)
	}
	if resp.Created != 0 || len(resp.Errors) != 1 {
		t.Fatalf("import created=%d errors=%v, want 0 created and 1 row error", resp.Created, resp.Errors)
	}
	if !containsSub(resp.Errors[0].Reason, store.CodeMediaNotReadable) {
		t.Errorf("row error reason = %q, want it to mention %q", resp.Errors[0].Reason, store.CodeMediaNotReadable)
	}
	var notes, mapping int64
	if err := env.db.Model(&store.Note{}).Where("deck_id = ?", deck.ID).Count(&notes).Error; err != nil {
		t.Fatalf("count notes: %v", err)
	}
	if err := env.db.Model(&store.MediaNote{}).Where("media_sha = ?", sha).Count(&mapping).Error; err != nil {
		t.Fatalf("count media_notes: %v", err)
	}
	if notes != 0 || mapping != 0 {
		t.Errorf("rejected import left notes=%d mapping=%d, want 0/0", notes, mapping)
	}
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
