package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// seedNote 在卡组下建一张 basic note，返回它（已带对外 id）。
func seedNote(t *testing.T, env *testEnv, deckID, ownerID uint64, front string) *store.Note {
	t.Helper()
	n := &store.Note{DeckID: deckID, Kind: "basic", CreatedBy: store.Ptr(ownerID)}
	if _, err := store.NewNoteStore(env.db).Create(context.Background(), n,
		map[string]any{"front": front, "back": "a"}); err != nil {
		t.Fatalf("create note: %v", err)
	}
	return n
}

// TestGetNoteReadsBeyondFirstPage 是 AUDIT-01 的回归：编辑页按对外 id 直接读目标卡片，
// 位置在单页之外的卡片同样读得到——修复前编辑页只拉第一页再去 find，第 101 条之后永远打不开。
func TestGetNoteReadsBeyondFirstPage(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "note_reader", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	key := seedKey(t, env.keys, user.ID, []string{store.ScopeRead}, nil)
	router := env.router()

	first := seedNote(t, env, deck.ID, user.ID, "first")
	second := seedNote(t, env, deck.ID, user.ID, "second")

	// 单页只放一条：second 不在第一页（列表按 created_at DESC, id DESC，second 在首页，first 不在）。
	listPath := fmt.Sprintf("/api/v1/decks/%s/notes?page=1&per_page=1", deck.PublicID)
	status, raw := doJSON(t, router, http.MethodGet, listPath, key.Plaintext, "")
	if status != http.StatusOK {
		t.Fatalf("list status = %d, want 200 (body %s)", status, raw)
	}
	var listed struct {
		Notes []map[string]any `json:"notes"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("list response not JSON: %v (%s)", err, raw)
	}
	if len(listed.Notes) != 1 {
		t.Fatalf("page 1 has %d notes, want 1 (fixture assumes a single-item page)", len(listed.Notes))
	}
	onPage, _ := listed.Notes[0]["id"].(string)
	offPage := first.PublicID
	if onPage == offPage {
		offPage = second.PublicID
	}

	// 不在首页的那条仍然按 id 直接读得到。
	status, raw = doJSON(t, router, http.MethodGet, "/api/v1/notes/"+offPage, key.Plaintext, "")
	if status != http.StatusOK {
		t.Fatalf("get off-page note status = %d, want 200 (body %s)", status, raw)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("get note response not JSON: %v (%s)", err, raw)
	}
	if got["id"] != offPage {
		t.Errorf("get note id = %v, want %q", got["id"], offPage)
	}
	if got["deck_id"] != deck.PublicID {
		t.Errorf("get note deck_id = %v, want %q", got["deck_id"], deck.PublicID)
	}
}

// TestGetNoteAuthorization 覆盖反面用例：未知 id、非成员、以及只有写 scope 的 key。
func TestGetNoteAuthorization(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "note_owner", store.RoleUser)
	outsider := seedUser(t, env.db, "note_outsider", store.RoleUser)
	deck, note := newDeckWithNote(t, env, owner.ID)
	router := env.router()
	ctx := context.Background()

	// 未知 id → 404 not_found。
	readKey := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead}, nil)
	status, raw := doJSON(t, router, http.MethodGet, "/api/v1/notes/does-not-exist", readKey.Plaintext, "")
	if status != http.StatusNotFound {
		t.Fatalf("unknown id status = %d, want 404 (body %s)", status, raw)
	}

	// 非成员读别人的卡组卡片 → 403/404（不泄露存在性）。
	outsiderKey := seedKey(t, env.keys, outsider.ID, []string{store.ScopeRead}, nil)
	status, raw = doJSON(t, router, http.MethodGet, "/api/v1/notes/"+note.PublicID, outsiderKey.Plaintext, "")
	if status != http.StatusForbidden && status != http.StatusNotFound {
		t.Fatalf("outsider status = %d, want 403/404 (body %s)", status, raw)
	}
	if _, _, err := env.api.ListNotes(ctx, outsider.ID, deck.ID, store.NoteListOptions{}); err == nil {
		t.Error("outsider ListNotes succeeded, want an error (fixture sanity)")
	}

	// 只有写 scope 的 key 调读接口 → 403 scope_required。
	writeKey := seedKey(t, env.keys, owner.ID, []string{store.ScopeWrite}, nil)
	status, raw = doJSON(t, router, http.MethodGet, "/api/v1/notes/"+note.PublicID, writeKey.Plaintext, "")
	if status != http.StatusForbidden {
		t.Fatalf("write-only key read status = %d, want 403 (body %s)", status, raw)
	}
}

// TestGetNoteAllowsReader 是正例：共享卡组的 reader 能按 id 读取卡片内容（能看就能编辑表单回填）。
func TestGetNoteAllowsReader(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "read_owner", store.RoleUser)
	reader := seedUser(t, env.db, "read_reader", store.RoleUser)
	deck, note := newDeckWithNote(t, env, owner.ID)
	if err := store.NewGrantStore(env.db).Grant(context.Background(), deck.ID, reader.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant reader: %v", err)
	}
	got, err := env.api.GetNote(context.Background(), reader.ID, note.PublicID)
	if err != nil {
		t.Fatalf("reader GetNote error = %v, want nil", err)
	}
	if got.ID != note.ID {
		t.Errorf("reader GetNote id = %d, want %d", got.ID, note.ID)
	}
}
