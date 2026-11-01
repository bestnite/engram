package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestBulkImportKeepsUnnamedNoteMetadata is the regression for the batch update that erased a note's
// source_url and extra_json.
//
// The note is created with both columns set, then rewritten through the import while naming only its
// fields. A second pass names an empty source_url, which must clear it: the request distinguishes "not
// mentioned, keep it" from "explicitly empty".
func TestBulkImportKeepsUnnamedNoteMetadata(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "bulk_metadata_keep", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	path := fmt.Sprintf("/api/v1/decks/%s/notes", deck.PublicID)
	noteType := createStockNoteType(t, env.db, deck.ID, "basic")

	note := store.Note{
		DeckID: deck.ID, NoteTypeID: store.Ptr(noteType.ID), TagsJSON: "[]",
		ExternalRef: store.Ptr("meta:1"),
		SourceURL:   store.Ptr("https://example.com/a"),
		ExtraJSON:   store.Ptr(`{"k":"v"}`),
	}
	if _, err := store.NewNoteStore(env.db).Create(t.Context(), &note, map[string]any{
		"front": "old question", "back": "old answer",
	}); err != nil {
		t.Fatalf("create note: %v", err)
	}

	body := fmt.Sprintf(`{"notes":[{"note_type":%q,"fields":{"front":"new question","back":"new answer"},"external_ref":"meta:1"}]}`,
		noteType.PublicID)
	status, raw := doJSON(t, router, http.MethodPost, path, k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("import status = %d, want 200 (body %s)", status, raw)
	}
	var resp importResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("import response is not JSON: %v (%s)", err, raw)
	}
	if resp.Updated != 1 || len(resp.Errors) != 0 {
		t.Fatalf("import = updated %d errors %+v, want 1/0", resp.Updated, resp.Errors)
	}

	var after store.Note
	if err := env.db.First(&after, note.ID).Error; err != nil {
		t.Fatalf("reload note: %v", err)
	}
	if after.SourceURL == nil || *after.SourceURL != "https://example.com/a" {
		t.Errorf("source_url after import = %v, want the stored value kept", after.SourceURL)
	}
	if after.ExtraJSON == nil || *after.ExtraJSON != `{"k":"v"}` {
		t.Errorf("extra_json after import = %v, want the stored value kept", after.ExtraJSON)
	}

	// An explicit empty source_url clears the column: absent and empty are not the same request.
	body = fmt.Sprintf(`{"notes":[{"note_type":%q,"fields":{"front":"newer question","back":"newer answer"},"external_ref":"meta:1","source_url":""}]}`,
		noteType.PublicID)
	status, raw = doJSON(t, router, http.MethodPost, path, k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("clearing import status = %d, want 200 (body %s)", status, raw)
	}
	if err := env.db.First(&after, note.ID).Error; err != nil {
		t.Fatalf("reload note after clearing import: %v", err)
	}
	if after.SourceURL != nil {
		t.Errorf("source_url after an empty value = %v, want it cleared", after.SourceURL)
	}
	if after.ExtraJSON == nil || *after.ExtraJSON != `{"k":"v"}` {
		t.Errorf("extra_json after the second import = %v, want it still kept", after.ExtraJSON)
	}
}

// TestBulkImportWritesTopLevelMetadata is the other half: source_url and extra sent as the schema declares
// them at note level must reach their columns on a create.
func TestBulkImportWritesTopLevelMetadata(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "bulk_metadata_land", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	path := fmt.Sprintf("/api/v1/decks/%s/notes", deck.PublicID)
	noteType := createStockNoteType(t, env.db, deck.ID, "basic").PublicID

	body := fmt.Sprintf(`{"notes":[{"note_type":%q,"fields":{"front":"q","back":"a"},`+
		`"external_ref":"meta:2","source_url":"https://example.com/source","extra":{"licence":"example"}}]}`, noteType)
	status, raw := doJSON(t, router, http.MethodPost, path, k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("import status = %d, want 200 (body %s)", status, raw)
	}
	var resp importResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("import response is not JSON: %v (%s)", err, raw)
	}
	if resp.Created != 1 || len(resp.Errors) != 0 {
		t.Fatalf("import = created %d errors %+v, want 1/0", resp.Created, resp.Errors)
	}

	var stored store.Note
	if err := env.db.Where("deck_id = ? AND external_ref = ?", deck.ID, "meta:2").First(&stored).Error; err != nil {
		t.Fatalf("load imported note: %v", err)
	}
	if stored.SourceURL == nil || *stored.SourceURL != "https://example.com/source" {
		t.Errorf("source_url = %v, want the top-level value written", stored.SourceURL)
	}
	if stored.ExtraJSON == nil || *stored.ExtraJSON != `{"licence":"example"}` {
		t.Errorf("extra_json = %v, want the top-level value written", stored.ExtraJSON)
	}
}
