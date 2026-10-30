package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/store"
)

// testNoteTypeConfig is a valid normal note type whose field ids differ from their names, so a test
// can tell "the server mapped the field name to the field id" from "the id was used by accident".
func testNoteTypeConfig() notetype.Config {
	return notetype.Config{
		Kind: notetype.KindNormal,
		Fields: []notetype.Field{
			{ID: "f-front", Name: "Front"},
			{ID: "f-back", Name: "Back"},
		},
		Templates: []notetype.Template{
			{ID: "card-1", Name: "Card 1", QFormat: "{{Front}}", AFormat: "{{Back}}"},
		},
		CSS:         ".card { color: black; }",
		SortFieldID: "f-front",
	}
}

// noteTypeBody renders a create/update body; version nil omits it.
func noteTypeBody(name string, cfg notetype.Config, version *int) string {
	body := map[string]any{"name": name, "config": cfg}
	if version != nil {
		body["version"] = *version
	}
	raw, _ := json.Marshal(body)
	return string(raw)
}

// createNoteTypeREST creates a note type through the endpoint and returns its response body.
func createNoteTypeREST(t *testing.T, router http.Handler, deckPath, key, name string, cfg notetype.Config) NoteTypeJSON {
	t.Helper()
	code, raw := doJSON(t, router, http.MethodPost, deckPath, key, noteTypeBody(name, cfg, nil))
	if code != http.StatusCreated {
		t.Fatalf("POST %s = %d, want 201 (body %s)", deckPath, code, raw)
	}
	var nt NoteTypeJSON
	if err := json.Unmarshal(raw, &nt); err != nil {
		t.Fatalf("create note type response is not JSON: %v (%s)", err, raw)
	}
	return nt
}

// TestNoteTypeCRUD covers list, get, create, update and delete under a deck, including the optimistic
// lock (a stale version is 409) and the refused delete while a note still uses the type.
func TestNoteTypeCRUD(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	owner := seedUser(t, env.db, "note_type_owner", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead, store.ScopeWrite}, nil).Plaintext
	base := fmt.Sprintf("/api/v1/decks/%s/note-types", deck.PublicID)

	created := createNoteTypeREST(t, router, base, key, "Custom", testNoteTypeConfig())
	if created.ID == "" || created.Version != 0 || created.Name != "Custom" || len(created.Config.Fields) != 2 {
		t.Fatalf("created note type = %+v, want an id, version 0 and the config", created)
	}
	if created.Config.Fields[0].ID != "f-front" || created.Config.Fields[0].Name != "Front" {
		t.Fatalf("created field = %+v, want id f-front and name Front", created.Config.Fields[0])
	}

	// List: only this deck's type.
	code, raw := doJSON(t, router, http.MethodGet, base, key, "")
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200 (body %s)", base, code, raw)
	}
	var list NoteTypeListResponse
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("list response is not JSON: %v (%s)", err, raw)
	}
	if len(list.NoteTypes) != 1 || list.NoteTypes[0].ID != created.ID {
		t.Fatalf("list = %+v, want the one created type", list.NoteTypes)
	}

	// Get one.
	onePath := base + "/" + created.ID
	code, raw = doJSON(t, router, http.MethodGet, onePath, key, "")
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200 (body %s)", onePath, code, raw)
	}
	var got NoteTypeJSON
	if err := json.Unmarshal(raw, &got); err != nil || got.ID != created.ID {
		t.Fatalf("GET %s = %s (%v), want the created type", onePath, raw, err)
	}

	// Update with the version read: succeeds and increments the version.
	version := 0
	code, raw = doJSON(t, router, http.MethodPatch, onePath, key, noteTypeBody("Renamed", testNoteTypeConfig(), &version))
	if code != http.StatusOK {
		t.Fatalf("PATCH %s = %d, want 200 (body %s)", onePath, code, raw)
	}
	var updated NoteTypeJSON
	if err := json.Unmarshal(raw, &updated); err != nil || updated.Version != 1 || updated.Name != "Renamed" {
		t.Fatalf("PATCH = %s (%v), want version 1 and name Renamed", raw, err)
	}

	// The same stale version again: 409 version_conflict.
	code, raw = doJSON(t, router, http.MethodPatch, onePath, key, noteTypeBody("Stale", testNoteTypeConfig(), &version))
	if code != http.StatusConflict {
		t.Fatalf("stale PATCH = %d, want 409 (body %s)", code, raw)
	}
	assertErrorCode(t, raw, CodeVersionConflict)

	// A missing version is a request error, not a blind overwrite.
	code, raw = doJSON(t, router, http.MethodPatch, onePath, key, noteTypeBody("No version", testNoteTypeConfig(), nil))
	if code != http.StatusBadRequest {
		t.Fatalf("PATCH without version = %d, want 400 (body %s)", code, raw)
	}
	assertErrorCode(t, raw, CodeInvalidRequest)

	// A note that uses the type blocks the delete with a stable code.
	noteBody := fmt.Sprintf(`{"notes":[{"note_type":%q,"fields":{"Front":"<b>Q</b>","Back":"A"}}]}`, created.ID)
	code, raw = doJSON(t, router, http.MethodPost, fmt.Sprintf("/api/v1/decks/%s/notes", deck.PublicID), key, noteBody)
	if code != http.StatusOK {
		t.Fatalf("create note with note_type = %d, want 200 (body %s)", code, raw)
	}
	code, raw = doJSON(t, router, http.MethodDelete, onePath, key, "")
	if code != http.StatusConflict {
		t.Fatalf("DELETE used note type = %d, want 409 (body %s)", code, raw)
	}
	assertErrorCode(t, raw, CodeNoteTypeInUse)

	// An unused type deletes normally, and is gone afterwards.
	spare := createNoteTypeREST(t, router, base, key, "Spare", testNoteTypeConfig())
	sparePath := base + "/" + spare.ID
	code, raw = doJSON(t, router, http.MethodDelete, sparePath, key, "")
	if code != http.StatusOK {
		t.Fatalf("DELETE unused note type = %d, want 200 (body %s)", code, raw)
	}
	code, raw = doJSON(t, router, http.MethodGet, sparePath, key, "")
	if code != http.StatusNotFound {
		t.Fatalf("GET deleted note type = %d, want 404 (body %s)", code, raw)
	}
}

// TestNoteTypeReaderCannotEdit is the role negative case: a reader may list and read note types but
// every write returns insufficient_role, while the type is left untouched.
func TestNoteTypeReaderCannotEdit(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	owner := seedUser(t, env.db, "note_type_owner_roles", store.RoleUser)
	reader := seedUser(t, env.db, "note_type_reader", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	if err := store.NewGrantStore(env.db).Grant(context.Background(), deck.ID, reader.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant reader: %v", err)
	}
	ownerKey := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead, store.ScopeWrite}, nil).Plaintext
	readerKey := seedKey(t, env.keys, reader.ID, []string{store.ScopeRead, store.ScopeWrite}, nil).Plaintext
	base := fmt.Sprintf("/api/v1/decks/%s/note-types", deck.PublicID)

	nt := createNoteTypeREST(t, router, base, ownerKey, "Owned", testNoteTypeConfig())

	// A reader may read.
	code, raw := doJSON(t, router, http.MethodGet, base, readerKey, "")
	if code != http.StatusOK {
		t.Fatalf("reader GET list = %d, want 200 (body %s)", code, raw)
	}
	onePath := base + "/" + nt.ID
	code, raw = doJSON(t, router, http.MethodGet, onePath, readerKey, "")
	if code != http.StatusOK {
		t.Fatalf("reader GET one = %d, want 200 (body %s)", code, raw)
	}

	// Every write is refused.
	version := 0
	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"create", http.MethodPost, base, noteTypeBody("Reader", testNoteTypeConfig(), nil)},
		{"update", http.MethodPatch, onePath, noteTypeBody("Reader", testNoteTypeConfig(), &version)},
		{"delete", http.MethodDelete, onePath, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, raw := doJSON(t, router, tc.method, tc.path, readerKey, tc.body)
			if code != http.StatusForbidden {
				t.Fatalf("reader %s = %d, want 403 (body %s)", tc.name, code, raw)
			}
			assertErrorCode(t, raw, CodeInsufficientRole)
		})
	}

	// The type still exists and still carries the owner's name.
	code, raw = doJSON(t, router, http.MethodGet, onePath, ownerKey, "")
	if code != http.StatusOK || !strings.Contains(string(raw), `"name":"Owned"`) {
		t.Fatalf("note type after refused writes = %d %s, want the untouched type", code, raw)
	}
}

// TestNotePayloadUsesNoteTypeAndFieldNames covers the new payload: a note is created and updated with
// {"note_type","fields"} where fields map field names to HTML, and the response carries the note type
// public id. An unknown field name is rejected with the name in the message.
func TestNotePayloadUsesNoteTypeAndFieldNames(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	owner := seedUser(t, env.db, "note_payload_owner", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead, store.ScopeWrite}, nil).Plaintext
	typesBase := fmt.Sprintf("/api/v1/decks/%s/note-types", deck.PublicID)
	nt := createNoteTypeREST(t, router, typesBase, key, "Custom", testNoteTypeConfig())

	notesPath := fmt.Sprintf("/api/v1/decks/%s/notes", deck.PublicID)
	body := fmt.Sprintf(`{"notes":[{"note_type":%q,"fields":{"Front":"<b>Question</b>","Back":"<i>Answer</i>"},"external_ref":"n:1"}]}`, nt.ID)
	code, raw := doJSON(t, router, http.MethodPost, notesPath, key, body)
	if code != http.StatusOK {
		t.Fatalf("POST note with note_type = %d, want 200 (body %s)", code, raw)
	}
	var imported ImportResponse
	if err := json.Unmarshal(raw, &imported); err != nil || imported.Created != 1 || len(imported.Errors) != 0 {
		t.Fatalf("import response = %s (%v), want created 1 without errors", raw, err)
	}

	// The stored value uses the field id, proving the name was mapped.
	var stored store.Note
	if err := env.db.Where("external_ref = ?", "n:1").First(&stored).Error; err != nil {
		t.Fatalf("load stored note: %v", err)
	}
	values, err := store.ParseFieldValues(stored.FieldsJSON)
	if err != nil {
		t.Fatalf("parse stored fields: %v", err)
	}
	if values["f-front"] != "<b>Question</b>" || values["f-back"] != "<i>Answer</i>" {
		t.Fatalf("stored fields = %v, want the HTML values keyed by field id", values)
	}

	// The response names the note type and keeps the transitional kind/fields for the current SPA.
	code, raw = doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/v1/notes/%s", stored.PublicID), key, "")
	if code != http.StatusOK {
		t.Fatalf("GET note = %d, want 200 (body %s)", code, raw)
	}
	var note map[string]any
	if err := json.Unmarshal(raw, &note); err != nil {
		t.Fatalf("note response is not JSON: %v (%s)", err, raw)
	}
	if note["note_type"] != nt.ID {
		t.Fatalf("note note_type = %v, want %q", note["note_type"], nt.ID)
	}

	// Update by note_type and field name: accepted, and the content is replaced.
	updateBody := fmt.Sprintf(`{"note_type":%q,"fields":{"Front":"<b>New</b>","Back":"B"}}`, nt.ID)
	code, raw = doJSON(t, router, http.MethodPatch, fmt.Sprintf("/api/v1/notes/%s", stored.PublicID), key, updateBody)
	if code != http.StatusOK {
		t.Fatalf("PATCH note = %d, want 200 (body %s)", code, raw)
	}

	// An unknown field name is rejected and names the field.
	badBody := fmt.Sprintf(`{"note_type":%q,"fields":{"Nope":"x"}}`, nt.ID)
	code, raw = doJSON(t, router, http.MethodPatch, fmt.Sprintf("/api/v1/notes/%s", stored.PublicID), key, badBody)
	if code != http.StatusBadRequest {
		t.Fatalf("PATCH note with unknown field = %d, want 400 (body %s)", code, raw)
	}
	assertErrorCode(t, raw, CodeInvalidRequest)
	if !strings.Contains(string(raw), "Nope") {
		t.Fatalf("unknown-field message = %s, want the field name Nope", raw)
	}

	// An unknown note type id is a request error, not a 500.
	badTypeBody := `{"note_type":"0198c0de-0000-7000-8000-000000000000","fields":{"Front":"x","Back":"y"}}`
	code, raw = doJSON(t, router, http.MethodPatch, fmt.Sprintf("/api/v1/notes/%s", stored.PublicID), key, badTypeBody)
	if code != http.StatusBadRequest {
		t.Fatalf("PATCH note with unknown note_type = %d, want 400 (body %s)", code, raw)
	}
	assertErrorCode(t, raw, CodeInvalidRequest)
}

// TestNoteTypeValidationRejected covers a malformed config: it is a 400 that names the offending
// field, and nothing is written.
func TestNoteTypeValidationRejected(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	owner := seedUser(t, env.db, "note_type_invalid", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead, store.ScopeWrite}, nil).Plaintext
	base := fmt.Sprintf("/api/v1/decks/%s/note-types", deck.PublicID)

	cfg := testNoteTypeConfig()
	cfg.Fields[0].Name = "Front"
	cfg.Fields[1].Name = "Front" // duplicate name
	code, raw := doJSON(t, router, http.MethodPost, base, key, noteTypeBody("Broken", cfg, nil))
	if code != http.StatusBadRequest {
		t.Fatalf("POST invalid note type = %d, want 400 (body %s)", code, raw)
	}
	assertErrorCode(t, raw, CodeInvalidRequest)

	var count int64
	if err := env.db.Model(&store.NoteType{}).Where("deck_id = ?", deck.ID).Count(&count).Error; err != nil {
		t.Fatalf("count note types: %v", err)
	}
	if count != 0 {
		t.Fatalf("note types after rejected create = %d, want 0", count)
	}
}
