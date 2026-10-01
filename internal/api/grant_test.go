package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"example.com/flashcard/internal/store"
)

// newDeckWithNote 建一个卡组与一张 basic note，返回两者。
func newDeckWithNote(t *testing.T, env *testEnv, ownerID uint64) (*store.Deck, *store.Note) {
	t.Helper()
	deck := seedDeck(t, env.db, ownerID)
	note := &store.Note{DeckID: deck.ID, Kind: "basic", CreatedBy: store.Ptr(ownerID)}
	if _, err := store.NewNoteStore(env.db).Create(context.Background(), note,
		map[string]any{"front": "q", "back": "a"}); err != nil {
		t.Fatalf("create note: %v", err)
	}
	return deck, note
}

// serviceCode 从 error 中取出稳定 code，便于断言越权分支。
func serviceCode(t *testing.T, err error) (int, string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error, got nil")
	}
	se := asServiceError(err)
	return se.Status, se.Code
}

// TestReaderCannotModifyNote 是 M5-1 的反面用例：reader 能看能复习，但不能改卡片内容。
func TestReaderCannotModifyNote(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "reader_owner", store.RoleUser)
	reader := seedUser(t, env.db, "reader_user", store.RoleUser)
	deck, note := newDeckWithNote(t, env, owner.ID)

	grants := store.NewGrantStore(env.db)
	if err := grants.Grant(context.Background(), deck.ID, reader.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant reader: %v", err)
	}
	ctx := context.Background()

	// 正例：reader 可以列出卡片（能看）。
	if _, _, err := env.api.ListNotes(ctx, reader.ID, deck.ID, store.NoteListOptions{}); err != nil {
		t.Fatalf("reader ListNotes error = %v, want nil", err)
	}

	// 反面：reader 更新卡片内容被拒，code = insufficient_role。
	_, err := env.api.UpdateNote(ctx, reader.ID, note.ID, nil, UpdateNoteInput{
		Kind:   "basic",
		Fields: map[string]any{"front": "hacked", "back": "hacked"},
	})
	status, code := serviceCode(t, err)
	if status != http.StatusForbidden || code != CodeInsufficientRole {
		t.Fatalf("reader UpdateNote = (%d,%q), want (403,%q)", status, code, CodeInsufficientRole)
	}

	// 反面：reader 删除卡片同样被拒。
	if _, err := env.api.DeleteNote(ctx, reader.ID, note.ID, nil); err != nil {
		if _, code := serviceCode(t, err); code != CodeInsufficientRole {
			t.Fatalf("reader DeleteNote code = %q, want %q", code, CodeInsufficientRole)
		}
	} else {
		t.Fatalf("reader DeleteNote succeeded, want insufficient_role")
	}

	// 确认内容未被改动。
	got, err := store.NewNoteStore(env.db).ByID(ctx, note.ID)
	if err != nil {
		t.Fatalf("reload note: %v", err)
	}
	fields, _ := store.ParseFields(got.FieldsJSON)
	if fields["front"] == "hacked" {
		t.Fatalf("reader mutated note content, want unchanged")
	}
}

// TestEditorCannotChangeDeckSettingsOrGrants 是 M5-1 的反面用例：
// editor 能改卡片内容，但改不了卡组设置（owner 级）与授权。
func TestEditorCannotChangeDeckSettingsOrGrants(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "editor_owner", store.RoleUser)
	editor := seedUser(t, env.db, "editor_user", store.RoleUser)
	deck, note := newDeckWithNote(t, env, owner.ID)

	grants := store.NewGrantStore(env.db)
	if err := grants.Grant(context.Background(), deck.ID, editor.ID, store.RoleEditor, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant editor: %v", err)
	}
	ctx := context.Background()

	// 正例：editor 可以改卡片内容。
	if _, err := env.api.UpdateNote(ctx, editor.ID, note.ID, nil, UpdateNoteInput{
		Kind:   "basic",
		Fields: map[string]any{"front": "edited", "back": "edited"},
	}); err != nil {
		t.Fatalf("editor UpdateNote error = %v, want nil", err)
	}

	// 反面：editor 达不到 owner 门槛（卡组设置 / 授权都是 owner 级），code = insufficient_role。
	_, err := env.api.RequireDeckRole(ctx, editor.ID, deck.ID, store.RoleOwner)
	status, code := serviceCode(t, err)
	if status != http.StatusForbidden || code != CodeInsufficientRole {
		t.Fatalf("editor RequireDeckRole(owner) = (%d,%q), want (403,%q)", status, code, CodeInsufficientRole)
	}
}

// TestUnauthorizedCannotSeeDeck 覆盖“未授权用户看不到卡组”：无任何授权的用户连读都被拒（403 forbidden）。
func TestUnauthorizedCannotSeeDeck(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "stranger_owner", store.RoleUser)
	stranger := seedUser(t, env.db, "stranger_user", store.RoleUser)
	deck, _ := newDeckWithNote(t, env, owner.ID)
	ctx := context.Background()

	_, err := env.api.RequireDeckRole(ctx, stranger.ID, deck.ID, store.RoleReader)
	status, code := serviceCode(t, err)
	if status != http.StatusForbidden || code != CodeForbidden {
		t.Fatalf("stranger RequireDeckRole = (%d,%q), want (403,%q)", status, code, CodeForbidden)
	}

	if _, _, err := env.api.ListNotes(ctx, stranger.ID, deck.ID, store.NoteListOptions{}); err == nil {
		t.Fatalf("stranger ListNotes succeeded, want forbidden")
	}
}

// TestRevokeTakesEffectImmediately 覆盖 §5“撤销立即生效”：撤销后下一个请求即被拒。
func TestRevokeTakesEffectImmediately(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "revoke_owner", store.RoleUser)
	member := seedUser(t, env.db, "revoke_user", store.RoleUser)
	deck, _ := newDeckWithNote(t, env, owner.ID)
	ctx := context.Background()
	grants := store.NewGrantStore(env.db)

	if err := grants.Grant(ctx, deck.ID, member.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if _, err := env.api.RequireDeckRole(ctx, member.ID, deck.ID, store.RoleReader); err != nil {
		t.Fatalf("granted read denied: %v", err)
	}

	// 撤销后，紧接着的同一个请求被判 403。
	if err := grants.Revoke(ctx, deck.ID, member.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, err := env.api.RequireDeckRole(ctx, member.ID, deck.ID, store.RoleReader)
	status, code := serviceCode(t, err)
	if status != http.StatusForbidden || code != CodeForbidden {
		t.Fatalf("after revoke = (%d,%q), want (403,%q)", status, code, CodeForbidden)
	}
}

// TestHTTPReaderUpdateReturnsStableCode 从 HTTP 层确认越权响应体带稳定英文 code。
func TestHTTPReaderUpdateReturnsStableCode(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "http_owner", store.RoleUser)
	reader := seedUser(t, env.db, "http_reader", store.RoleUser)
	deck, note := newDeckWithNote(t, env, owner.ID)
	if err := store.NewGrantStore(env.db).Grant(context.Background(), deck.ID, reader.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant reader: %v", err)
	}
	// reader 持有 write scope 的 key，但卡组角色不够 —— 两道门都要过。
	key := seedKey(t, env.keys, reader.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	path := "/api/v1/notes/" + strconv.FormatUint(note.ID, 10)

	status, raw := doJSON(t, router, http.MethodPatch, path, key.Plaintext,
		`{"kind":"basic","fields":{"front":"x","back":"y"}}`)
	if status != http.StatusForbidden {
		t.Fatalf("reader PATCH note status = %d, want 403 (body %s)", status, raw)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("response not JSON: %v (%s)", err, raw)
	}
	if body.Error.Code != CodeInsufficientRole {
		t.Fatalf("reader PATCH note code = %q, want %q (body %s)", body.Error.Code, CodeInsufficientRole, raw)
	}
}
