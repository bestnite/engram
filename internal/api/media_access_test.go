package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// This file covers L2 privilege injection on the REST entries: an editor of a shared deck injects a reference to someone else's media into a note
// they can edit, and the pre-write check must reject it (stable code media_not_readable), leaving no dirty data in the database.

// seedMedia writes one media file to disk and returns its sha256 (registering the uploader's ownership).
func seedMedia(t *testing.T, env *testEnv, ownerID uint64, payload string) string {
	t.Helper()
	raw := append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, []byte(payload)...)
	m, err := store.NewMediaStore(env.db).SaveBytes(context.Background(), t.TempDir(), "image/png", raw, store.Ptr(ownerID))
	if err != nil {
		t.Fatalf("SaveBytes: %v", err)
	}
	return m.Sha256
}

// seedPlainNote creates a note with no media reference through the write method.
func seedPlainNote(t *testing.T, env *testEnv, deckID, authorID uint64) *store.Note {
	t.Helper()
	n := store.Note{DeckID: deckID, NoteTypeID: store.Ptr(stockNoteTypeID(t, env.db, deckID, "basic")), CreatedBy: store.Ptr(authorID)}
	if _, err := store.NewNoteStore(env.db).Save(context.Background(), &n, map[string]any{"front": "plain", "back": "x"}); err != nil {
		t.Fatalf("save note: %v", err)
	}
	return &n
}

// TestRESTUpdateNoteRejectsUnreadableMediaInjection covers the pre-write check of REST `PATCH /notes/:id`.
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
	body := fmt.Sprintf(`{"fields":{"front":"<img src=\"%s.png\">","back":"x"}}`, sha)

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

// TestRESTBulkImportRejectsUnreadableMediaInjection covers REST `POST /decks/:id/notes` (bulk import) and
// its pre-write check: the row carrying the unauthorized reference reports the stable code and is not persisted.
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
	body := fmt.Sprintf(`{"notes":[{"kind":"basic","fields":{"front":"<img src=\"%s.png\">","back":"x"}}]}`, sha)

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
