package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestSuspensionIsPerUserOverREST 覆盖暂停接口：共享卡组的读者暂停一张卡，只有他自己的队列与
// 笔记列表受影响，属主不受影响；取消暂停后恢复。负例：无关用户 404、缺 review scope 403。
func TestSuspensionIsPerUserOverREST(t *testing.T) {
	env := newTestEnv(t, 600, 600)
	ctx := context.Background()
	owner := seedUser(t, env.db, "susp-owner", store.RoleUser)
	reader := seedUser(t, env.db, "susp-reader", store.RoleUser)
	stranger := seedUser(t, env.db, "susp-stranger", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	card := seedKindCard(t, env.db, deck.ID, "basic", `{"front":"q","back":"a"}`, "forward")
	if err := store.NewGrantStore(env.db).Grant(ctx, deck.ID, reader.ID, store.RoleReader, &owner.ID); err != nil {
		t.Fatalf("Grant() error = %v", err)
	}
	var note store.Note
	if err := env.db.First(&note, card.NoteID).Error; err != nil {
		t.Fatal(err)
	}
	ownerKey := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead, store.ScopeReview}, nil)
	readerKey := seedKey(t, env.keys, reader.ID, []string{store.ScopeRead, store.ScopeReview}, nil)
	readOnlyKey := seedKey(t, env.keys, reader.ID, []string{store.ScopeRead}, nil)
	strangerKey := seedKey(t, env.keys, stranger.ID, []string{store.ScopeRead, store.ScopeReview}, nil)
	router := env.router()

	dueCount := func(key string) int {
		t.Helper()
		status, raw := doJSON(t, router, http.MethodGet, "/api/v1/review/due?deck="+deck.PublicID, key, "")
		if status != http.StatusOK {
			t.Fatalf("GET due = %d (%s)", status, raw)
		}
		var body struct {
			Cards []DueCard `json:"cards"`
		}
		_ = json.Unmarshal(raw, &body)
		return len(body.Cards)
	}
	noteSuspended := func(key string) bool {
		t.Helper()
		status, raw := doJSON(t, router, http.MethodGet, "/api/v1/decks/"+deck.PublicID+"/notes", key, "")
		if status != http.StatusOK {
			t.Fatalf("GET notes = %d (%s)", status, raw)
		}
		var body struct {
			Notes []map[string]any `json:"notes"`
		}
		_ = json.Unmarshal(raw, &body)
		return body.Notes[0]["suspended"] == true
	}

	path := "/api/v1/cards/" + card.PublicID + "/suspension"
	negatives := []struct {
		name string
		key  string
		want int
	}{
		{"stranger cannot suspend", strangerKey.Plaintext, http.StatusNotFound},
		{"key without review scope", readOnlyKey.Plaintext, http.StatusForbidden},
	}
	for _, tc := range negatives {
		if status, raw := doJSON(t, router, http.MethodPut, path, tc.key, ""); status != tc.want && !(tc.want == http.StatusNotFound && status == http.StatusForbidden) {
			t.Errorf("%s: PUT = %d, want %d (%s)", tc.name, status, tc.want, raw)
		}
	}

	if status, raw := doJSON(t, router, http.MethodPut, path, readerKey.Plaintext, ""); status != http.StatusOK {
		t.Fatalf("reader PUT suspension = %d (%s)", status, raw)
	}
	if dueCount(readerKey.Plaintext) != 0 || !noteSuspended(readerKey.Plaintext) {
		t.Error("reader still sees the card as active after suspending it")
	}
	if dueCount(ownerKey.Plaintext) != 1 || noteSuspended(ownerKey.Plaintext) {
		t.Error("the reader's suspension leaked to the owner")
	}

	if status, raw := doJSON(t, router, http.MethodDelete, "/api/v1/notes/"+note.PublicID+"/suspension", readerKey.Plaintext, ""); status != http.StatusOK {
		t.Fatalf("reader DELETE note suspension = %d (%s)", status, raw)
	}
	if dueCount(readerKey.Plaintext) != 1 || noteSuspended(readerKey.Plaintext) {
		t.Error("reader does not get the card back after unsuspending the note")
	}
	var audits int64
	env.db.Model(&store.AuditLog{}).Where("action IN ?", []string{store.ActionCardSuspend, store.ActionCardUnsuspend}).Count(&audits)
	if audits != 2 {
		t.Errorf("suspension audit rows = %d, want 2", audits)
	}
}
