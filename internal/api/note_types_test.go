package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

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

// TestNoteTypeDeletePurgesSoftDeletedNotes covers the purge path over REST: a type used only by soft-deleted
// notes is refused with the three counts until confirm_purge=true is sent, a reader is refused, the confirmed
// delete removes the type and its note, the review row stays, and the audit row records the counts.
func TestNoteTypeDeletePurgesSoftDeletedNotes(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	owner := seedUser(t, env.db, "note_type_purge_owner", store.RoleUser)
	reader := seedUser(t, env.db, "note_type_purge_reader", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	if err := store.NewGrantStore(env.db).Grant(context.Background(), deck.ID, reader.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant reader: %v", err)
	}
	ownerKey := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead, store.ScopeWrite}, nil).Plaintext
	readerKey := seedKey(t, env.keys, reader.ID, []string{store.ScopeRead, store.ScopeWrite}, nil).Plaintext
	typesBase := fmt.Sprintf("/api/v1/decks/%s/note-types", deck.PublicID)

	nt := createNoteTypeREST(t, router, typesBase, ownerKey, "Deleted notes only", testNoteTypeConfig())
	createNotesREST(t, router, deck.PublicID, nt.ID, ownerKey, []map[string]string{{"Front": "Q", "Back": "A"}})
	var typeRow store.NoteType
	if err := env.db.Where("public_id = ?", nt.ID).First(&typeRow).Error; err != nil {
		t.Fatalf("load note type: %v", err)
	}
	var note store.Note
	if err := env.db.Where("note_type_id = ?", typeRow.ID).First(&note).Error; err != nil {
		t.Fatalf("load note: %v", err)
	}
	var card store.Card
	if err := env.db.Where("note_id = ?", note.ID).First(&card).Error; err != nil {
		t.Fatalf("load card: %v", err)
	}
	if err := env.db.Create(&store.Review{
		CardID: card.ID, UserID: owner.ID, Rating: 3, GradeSource: "self",
		ReviewedAt: time.Now().UTC(), ReviewDay: "2026-10-31", StateBefore: 2,
	}).Error; err != nil {
		t.Fatalf("create review: %v", err)
	}
	// Soft-delete the note: the only remaining reference to the type is now a deleted note.
	if err := env.db.Delete(&store.Note{}, note.ID).Error; err != nil {
		t.Fatalf("soft delete note: %v", err)
	}
	onePath := typesBase + "/" + nt.ID

	// A reader is refused even when the request would confirm the purge, before any check of the note state.
	code, raw := doJSON(t, router, http.MethodDelete, onePath+"?confirm_purge=true", readerKey, "")
	if code != http.StatusForbidden {
		t.Fatalf("reader DELETE = %d, want 403 (body %s)", code, raw)
	}
	assertErrorCode(t, raw, CodeInsufficientRole)

	// Without confirmation: 409 with the stable code and the counts, and nothing is written.
	code, raw = doJSON(t, router, http.MethodDelete, onePath, ownerKey, "")
	if code != http.StatusConflict {
		t.Fatalf("DELETE soft-deleted-only type = %d, want 409 (body %s)", code, raw)
	}
	assertErrorCode(t, raw, CodeNoteTypePurgeUnconfirmed)
	for _, want := range []string{`"deleted_notes":1`, `"cards":1`, `"reviews":1`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("refusal body %s does not carry %s", raw, want)
		}
	}
	code, raw = doJSON(t, router, http.MethodGet, onePath, ownerKey, "")
	if code != http.StatusOK {
		t.Fatalf("GET after refused purge = %d, want 200 (body %s)", code, raw)
	}

	// The confirmation removes the type and its note.
	code, raw = doJSON(t, router, http.MethodDelete, onePath+"?confirm_purge=true", ownerKey, "")
	if code != http.StatusOK {
		t.Fatalf("confirmed DELETE = %d, want 200 (body %s)", code, raw)
	}
	code, raw = doJSON(t, router, http.MethodGet, onePath, ownerKey, "")
	if code != http.StatusNotFound {
		t.Fatalf("GET purged type = %d, want 404 (body %s)", code, raw)
	}

	// The audit row records the three counts.
	var audit store.AuditLog
	if err := env.db.Where("action = ?", store.ActionNoteTypeDelete).Order("id DESC").First(&audit).Error; err != nil {
		t.Fatalf("load audit row: %v", err)
	}
	for _, want := range []string{`"deleted_notes":1`, `"cards":1`, `"reviews":1`} {
		if audit.DetailJSON == nil || !strings.Contains(*audit.DetailJSON, want) {
			t.Errorf("audit detail = %v, want %s", audit.DetailJSON, want)
		}
	}

	// The review log survives the purge.
	var reviews int64
	if err := env.db.Model(&store.Review{}).Where("card_id = ?", card.ID).Count(&reviews).Error; err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if reviews != 1 {
		t.Errorf("reviews after purge = %d, want 1", reviews)
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

// testAnswerNoteTypeConfig is a valid config whose template carries an answer spec bound to a declared field.
func testAnswerNoteTypeConfig() notetype.Config {
	cfg := testNoteTypeConfig()
	cfg.Templates[0].QFormat = "{{Front}}{{type:Back}}"
	cfg.Templates[0].Answer = &notetype.AnswerSpec{
		Control:  "numeric",
		Bindings: map[string]string{"value": "f-back"},
	}
	return cfg
}

// restErrorMessage decodes the message of one REST error response, so assertions run on the unescaped text.
func restErrorMessage(t *testing.T, raw []byte) string {
	t.Helper()
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("error response is not JSON: %v (%s)", err, raw)
	}
	return body.Error.Message
}

// TestNoteTypeAnswerSpecValidationRejected covers the three answer-spec violations through create and update: an
// unknown control, a missing required role and a role bound to an undeclared field id. Each is a 400 whose message
// names the template and the offending role (the control, when no role exists), and the rejected write changes nothing.
func TestNoteTypeAnswerSpecValidationRejected(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*notetype.Config)
		want   []string
	}{
		{
			name:   "unknown control",
			mutate: func(cfg *notetype.Config) { cfg.Templates[0].Answer.Control = "does_not_exist" },
			want:   []string{`template "Card 1"`, `"does_not_exist"`},
		},
		{
			name:   "missing required role",
			mutate: func(cfg *notetype.Config) { cfg.Templates[0].Answer = &notetype.AnswerSpec{Control: "numeric"} },
			want:   []string{`template "Card 1"`, `role "value"`},
		},
		{
			name:   "role bound to an undeclared field id",
			mutate: func(cfg *notetype.Config) { cfg.Templates[0].Answer.Bindings["value"] = "f-missing" },
			want:   []string{`template "Card 1"`, `role "value"`, `"f-missing"`},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := newTestEnv(t, 1000, 1000)
			router := env.router()
			owner := seedUser(t, env.db, "note_type_answer_spec", store.RoleUser)
			deck := seedDeck(t, env.db, owner.ID)
			key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead, store.ScopeWrite}, nil).Plaintext
			base := fmt.Sprintf("/api/v1/decks/%s/note-types", deck.PublicID)

			bad := testAnswerNoteTypeConfig()
			test.mutate(&bad)
			code, raw := doJSON(t, router, http.MethodPost, base, key, noteTypeBody("Broken", bad, nil))
			if code != http.StatusBadRequest {
				t.Fatalf("POST invalid answer spec = %d, want 400 (body %s)", code, raw)
			}
			assertErrorCode(t, raw, CodeInvalidRequest)
			message := restErrorMessage(t, raw)
			for _, want := range test.want {
				if !strings.Contains(message, want) {
					t.Errorf("POST message = %q, want substring %q", message, want)
				}
			}
			var count int64
			if err := env.db.Model(&store.NoteType{}).Where("deck_id = ?", deck.ID).Count(&count).Error; err != nil {
				t.Fatalf("count note types: %v", err)
			}
			if count != 0 {
				t.Fatalf("rejected create wrote %d note type row(s), want 0", count)
			}

			// Update: start from a valid type, then send the same invalid spec.
			created := createNoteTypeREST(t, router, base, key, "Answer type", testAnswerNoteTypeConfig())
			badUpdate := testAnswerNoteTypeConfig()
			test.mutate(&badUpdate)
			version := 0
			onePath := base + "/" + created.ID
			code, raw = doJSON(t, router, http.MethodPatch, onePath, key, noteTypeBody("Broken", badUpdate, &version))
			if code != http.StatusBadRequest {
				t.Fatalf("PATCH invalid answer spec = %d, want 400 (body %s)", code, raw)
			}
			assertErrorCode(t, raw, CodeInvalidRequest)
			message = restErrorMessage(t, raw)
			for _, want := range test.want {
				if !strings.Contains(message, want) {
					t.Errorf("PATCH message = %q, want substring %q", message, want)
				}
			}

			code, raw = doJSON(t, router, http.MethodGet, onePath, key, "")
			if code != http.StatusOK {
				t.Fatalf("GET after rejected PATCH = %d, want 200 (body %s)", code, raw)
			}
			var kept NoteTypeJSON
			if err := json.Unmarshal(raw, &kept); err != nil {
				t.Fatalf("GET after rejected PATCH is not JSON: %v (%s)", err, raw)
			}
			if kept.Version != 0 {
				t.Errorf("version after rejected PATCH = %d, want 0", kept.Version)
			}
			if len(kept.Config.Templates) != 1 || kept.Config.Templates[0].Answer == nil ||
				kept.Config.Templates[0].Answer.Control != "numeric" ||
				kept.Config.Templates[0].Answer.Bindings["value"] != "f-back" {
				t.Errorf("config after rejected PATCH = %+v, want the original answer spec", kept.Config.Templates)
			}
		})
	}
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

// noteTypePK resolves the internal primary key of a note type by its public id.
func noteTypePK(t *testing.T, env *testEnv, publicID string) uint64 {
	t.Helper()
	var nt store.NoteType
	if err := env.db.Where("public_id = ?", publicID).First(&nt).Error; err != nil {
		t.Fatalf("load note type %q: %v", publicID, err)
	}
	return nt.ID
}

// noteTypeUpdateREST sends a PATCH body for a note type, with optional extra body keys such as
// dry_run and confirm_card_removal, and returns the status and raw response.
func noteTypeUpdateREST(t *testing.T, router http.Handler, path, key, name string, cfg notetype.Config, version int, extra map[string]any) (int, []byte) {
	t.Helper()
	body := map[string]any{"name": name, "config": cfg, "version": version}
	for k, v := range extra {
		body[k] = v
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal update body: %v", err)
	}
	return doJSON(t, router, http.MethodPatch, path, key, string(raw))
}

// noteTypeUpdateResponseJSON is the PATCH response: the note type fields sit at the top level (the
// embedded NoteTypeJSON), followed by the card impact counts.
type noteTypeUpdateResponseJSON struct {
	NoteTypeJSON
	DryRun                bool `json:"dry_run"`
	CardsAdded            int  `json:"cards_added"`
	CardsRemoved          int  `json:"cards_removed"`
	ReviewsOnRemovedCards int  `json:"reviews_on_removed_cards"`
}

// decodeNoteTypeUpdate decodes a successful PATCH response.
func decodeNoteTypeUpdate(t *testing.T, raw []byte) noteTypeUpdateResponseJSON {
	t.Helper()
	var out noteTypeUpdateResponseJSON
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("update response is not JSON: %v (%s)", err, raw)
	}
	return out
}

// noteTypeCards loads the cards of a note type, ordered by note and ordinal. When includeDeleted is
// false only visible cards are returned; when it is true soft-deleted cards are included too.
func noteTypeCards(t *testing.T, env *testEnv, noteTypeID uint64, includeDeleted bool) []store.Card {
	t.Helper()
	q := env.db.Model(&store.Card{}).
		Joins("JOIN notes ON notes.id = cards.note_id").
		Where("notes.note_type_id = ? AND notes.deleted_at IS NULL", noteTypeID)
	if includeDeleted {
		q = q.Unscoped()
	}
	var cards []store.Card
	if err := q.Order("cards.note_id ASC, cards.ordinal ASC, cards.id ASC").Find(&cards).Error; err != nil {
		t.Fatalf("load cards of note type %d: %v", noteTypeID, err)
	}
	return cards
}

// createNotesREST creates the given field maps as notes of one note type through the note endpoint.
func createNotesREST(t *testing.T, router http.Handler, deckPublicID, noteTypeID, key string, fields []map[string]string) {
	t.Helper()
	notes := make([]map[string]any, 0, len(fields))
	for _, f := range fields {
		notes = append(notes, map[string]any{"note_type": noteTypeID, "fields": f})
	}
	raw, err := json.Marshal(map[string]any{"notes": notes})
	if err != nil {
		t.Fatalf("marshal notes body: %v", err)
	}
	path := fmt.Sprintf("/api/v1/decks/%s/notes", deckPublicID)
	code, body := doJSON(t, router, http.MethodPost, path, key, string(raw))
	if code != http.StatusOK {
		t.Fatalf("POST %s = %d, want 200 (body %s)", path, code, body)
	}
}

// TestNoteTypeUpdateAddsCardsForNewTemplate: an update that adds a template creates one card per
// note whose new front is not empty, and reports the count.
func TestNoteTypeUpdateAddsCardsForNewTemplate(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	owner := seedUser(t, env.db, "note_type_add_cards", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead, store.ScopeWrite}, nil).Plaintext
	base := fmt.Sprintf("/api/v1/decks/%s/note-types", deck.PublicID)
	nt := createNoteTypeREST(t, router, base, key, "Cards", testNoteTypeConfig())
	ntPK := noteTypePK(t, env, nt.ID)

	// Two notes have a non-empty Front, the third does not. The initial template renders Front,
	// so only two cards exist before the update.
	createNotesREST(t, router, deck.PublicID, nt.ID, key, []map[string]string{
		{"Front": "Q1", "Back": "A1"},
		{"Front": "Q2", "Back": "A2"},
		{"Front": "", "Back": "A3"},
	})
	if got := len(noteTypeCards(t, env, ntPK, false)); got != 2 {
		t.Fatalf("cards before update = %d, want 2", got)
	}

	cfg := testNoteTypeConfig()
	cfg.Templates = append(cfg.Templates, notetype.Template{
		ID: "card-2", Name: "Card 2", QFormat: "{{Front}}", AFormat: "{{Back}}",
	})
	code, raw := noteTypeUpdateREST(t, router, base+"/"+nt.ID, key, "Cards", cfg, 0, nil)
	if code != http.StatusOK {
		t.Fatalf("PATCH adding template = %d, want 200 (body %s)", code, raw)
	}
	resp := decodeNoteTypeUpdate(t, raw)
	if resp.CardsAdded != 2 || resp.CardsRemoved != 0 || resp.ReviewsOnRemovedCards != 0 {
		t.Errorf("impact = added %d removed %d reviews %d, want 2/0/0",
			resp.CardsAdded, resp.CardsRemoved, resp.ReviewsOnRemovedCards)
	}
	if resp.Version != 1 {
		t.Errorf("version = %d, want 1", resp.Version)
	}

	cards := noteTypeCards(t, env, ntPK, false)
	if len(cards) != 4 {
		t.Fatalf("cards after update = %d, want 4", len(cards))
	}
	newCards := 0
	for _, card := range cards {
		if card.Template != "card-2" {
			continue
		}
		newCards++
		if card.Ordinal != 1 {
			t.Errorf("card-2 ordinal = %d, want 1 (the second template)", card.Ordinal)
		}
	}
	if newCards != 2 {
		t.Errorf("card-2 cards = %d, want 2 (only notes with a non-empty front)", newCards)
	}
}

// TestNoteTypeUpdateDryRunWritesNothing: a dry run reports both counts and leaves the note type,
// its cards and the audit trail untouched.
func TestNoteTypeUpdateDryRunWritesNothing(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	owner := seedUser(t, env.db, "note_type_dry_run", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead, store.ScopeWrite}, nil).Plaintext
	base := fmt.Sprintf("/api/v1/decks/%s/note-types", deck.PublicID)
	nt := createNoteTypeREST(t, router, base, key, "Dry", testNoteTypeConfig())
	ntPK := noteTypePK(t, env, nt.ID)
	onePath := base + "/" + nt.ID

	createNotesREST(t, router, deck.PublicID, nt.ID, key, []map[string]string{
		{"Front": "Q1", "Back": "A1"},
	})
	before := noteTypeCards(t, env, ntPK, true)
	if len(before) != 1 {
		t.Fatalf("cards before dry run = %d, want 1", len(before))
	}

	// Dry-run an addition: the count is reported, nothing is written.
	addCfg := testNoteTypeConfig()
	addCfg.Templates = append(addCfg.Templates, notetype.Template{
		ID: "card-2", Name: "Card 2", QFormat: "{{Back}}", AFormat: "{{Front}}",
	})
	code, raw := noteTypeUpdateREST(t, router, onePath, key, "Dry", addCfg, 0, map[string]any{"dry_run": true})
	if code != http.StatusOK {
		t.Fatalf("dry-run addition = %d, want 200 (body %s)", code, raw)
	}
	resp := decodeNoteTypeUpdate(t, raw)
	if !resp.DryRun || resp.CardsAdded != 1 || resp.CardsRemoved != 0 {
		t.Errorf("dry-run addition = (dry_run %v added %d removed %d), want (true, 1, 0)",
			resp.DryRun, resp.CardsAdded, resp.CardsRemoved)
	}
	if resp.Version != 0 {
		t.Errorf("dry-run response version = %d, want the stored 0", resp.Version)
	}

	// Dry-run a removal: the count is reported, the card and its review stay.
	card := before[0]
	if err := env.db.Create(&store.Review{
		CardID: card.ID, UserID: owner.ID, Rating: 3, GradeSource: "self",
		ReviewedAt: env.now, ReviewDay: env.api.reviewDay(owner), StateBefore: 0,
	}).Error; err != nil {
		t.Fatalf("create review: %v", err)
	}
	removeCfg := testNoteTypeConfig()
	removeCfg.Templates = []notetype.Template{}
	code, raw = noteTypeUpdateREST(t, router, onePath, key, "Dry", removeCfg, 0, map[string]any{"dry_run": true})
	if code != http.StatusOK {
		t.Fatalf("dry-run removal = %d, want 200 (body %s)", code, raw)
	}
	resp = decodeNoteTypeUpdate(t, raw)
	if !resp.DryRun || resp.CardsAdded != 0 || resp.CardsRemoved != 1 || resp.ReviewsOnRemovedCards != 1 {
		t.Errorf("dry-run removal = (dry_run %v added %d removed %d reviews %d), want (true, 0, 1, 1)",
			resp.DryRun, resp.CardsAdded, resp.CardsRemoved, resp.ReviewsOnRemovedCards)
	}

	// Nothing was written by either dry run: version, cards, reviews and audit are unchanged.
	stored, err := store.NewNoteTypeStore(env.db).ByID(t.Context(), ntPK)
	if err != nil {
		t.Fatalf("load note type: %v", err)
	}
	if stored.Version != 0 {
		t.Errorf("version after dry runs = %d, want 0", stored.Version)
	}
	after := noteTypeCards(t, env, ntPK, true)
	if len(after) != 1 || after[0].ID != card.ID || after[0].DeletedAt.Valid {
		t.Errorf("cards after dry runs = %+v, want the one original visible card", after)
	}
	var reviews int64
	if err := env.db.Model(&store.Review{}).Where("card_id = ?", card.ID).Count(&reviews).Error; err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if reviews != 1 {
		t.Errorf("reviews after dry runs = %d, want 1", reviews)
	}
	auditCount, err := store.NewAuditStore(env.db).CountByAction(t.Context(), store.ActionNoteTypeUpdate)
	if err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if auditCount != 0 {
		t.Errorf("note_type.update audit rows after dry runs = %d, want 0", auditCount)
	}
}

// TestNoteTypeUpdateRemovalRequiresConfirmation: removing a template without confirm_card_removal is
// a 409 that writes nothing; the same body with the confirmation soft-deletes the cards and keeps
// every review and card_states row.
func TestNoteTypeUpdateRemovalRequiresConfirmation(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	owner := seedUser(t, env.db, "note_type_remove_cards", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead, store.ScopeWrite}, nil).Plaintext
	base := fmt.Sprintf("/api/v1/decks/%s/note-types", deck.PublicID)

	cfg := testNoteTypeConfig()
	cfg.Templates = append(cfg.Templates, notetype.Template{
		ID: "card-2", Name: "Card 2", QFormat: "{{Back}}", AFormat: "{{Front}}",
	})
	nt := createNoteTypeREST(t, router, base, key, "Removable", cfg)
	ntPK := noteTypePK(t, env, nt.ID)
	onePath := base + "/" + nt.ID
	createNotesREST(t, router, deck.PublicID, nt.ID, key, []map[string]string{
		{"Front": "Q1", "Back": "A1"},
	})

	cards := noteTypeCards(t, env, ntPK, false)
	if len(cards) != 2 {
		t.Fatalf("cards before removal = %d, want 2", len(cards))
	}
	var removed store.Card
	for _, card := range cards {
		if card.Template == "card-2" {
			removed = card
		}
	}
	if removed.ID == 0 {
		t.Fatalf("no card-2 in %+v", cards)
	}
	if err := env.db.Create(&store.Review{
		CardID: removed.ID, UserID: owner.ID, Rating: 3, GradeSource: "self",
		ReviewedAt: env.now, ReviewDay: env.api.reviewDay(owner), StateBefore: 0,
	}).Error; err != nil {
		t.Fatalf("create review: %v", err)
	}
	due := env.now.AddDate(0, 0, 3)
	if err := env.db.Create(&store.CardState{
		CardID: removed.ID, UserID: owner.ID, State: "review", DueAt: &due,
	}).Error; err != nil {
		t.Fatalf("create card state: %v", err)
	}

	// Without the confirmation: 409 with the counts, and not a single row changes.
	removeCfg := testNoteTypeConfig()
	code, raw := noteTypeUpdateREST(t, router, onePath, key, "Removable", removeCfg, 0, nil)
	if code != http.StatusConflict {
		t.Fatalf("PATCH removing a template without confirmation = %d, want 409 (body %s)", code, raw)
	}
	var refusal struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		CardsAdded            int `json:"cards_added"`
		CardsRemoved          int `json:"cards_removed"`
		ReviewsOnRemovedCards int `json:"reviews_on_removed_cards"`
	}
	if err := json.Unmarshal(raw, &refusal); err != nil {
		t.Fatalf("refusal is not JSON: %v (%s)", err, raw)
	}
	if refusal.Error.Code != CodeCardRemovalUnconfirmed {
		t.Errorf("refusal code = %q, want %q", refusal.Error.Code, CodeCardRemovalUnconfirmed)
	}
	if refusal.CardsAdded != 0 || refusal.CardsRemoved != 1 || refusal.ReviewsOnRemovedCards != 1 {
		t.Errorf("refusal counts = added %d removed %d reviews %d, want 0/1/1",
			refusal.CardsAdded, refusal.CardsRemoved, refusal.ReviewsOnRemovedCards)
	}
	stored, err := store.NewNoteTypeStore(env.db).ByID(t.Context(), ntPK)
	if err != nil {
		t.Fatalf("load note type after refusal: %v", err)
	}
	if stored.Version != 0 {
		t.Errorf("version after refusal = %d, want 0 (the refused update writes nothing)", stored.Version)
	}
	if got := len(noteTypeCards(t, env, ntPK, false)); got != 2 {
		t.Errorf("visible cards after refusal = %d, want 2", got)
	}
	auditCount, err := store.NewAuditStore(env.db).CountByAction(t.Context(), store.ActionNoteTypeUpdate)
	if err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if auditCount != 0 {
		t.Errorf("note_type.update audit rows after refusal = %d, want 0", auditCount)
	}

	// With the confirmation: 200, the template's cards are soft-deleted, progress rows survive.
	code, raw = noteTypeUpdateREST(t, router, onePath, key, "Removable", removeCfg, 0,
		map[string]any{"confirm_card_removal": true})
	if code != http.StatusOK {
		t.Fatalf("PATCH removing a template with confirmation = %d, want 200 (body %s)", code, raw)
	}
	resp := decodeNoteTypeUpdate(t, raw)
	if resp.CardsAdded != 0 || resp.CardsRemoved != 1 || resp.ReviewsOnRemovedCards != 1 {
		t.Errorf("confirmed removal counts = added %d removed %d reviews %d, want 0/1/1",
			resp.CardsAdded, resp.CardsRemoved, resp.ReviewsOnRemovedCards)
	}
	if resp.Version != 1 {
		t.Errorf("version after confirmed removal = %d, want 1", resp.Version)
	}

	all := noteTypeCards(t, env, ntPK, true)
	if len(all) != 2 {
		t.Fatalf("stored cards after confirmed removal = %d, want 2 rows (one soft-deleted)", len(all))
	}
	for _, card := range all {
		switch card.Template {
		case "card-2":
			if !card.DeletedAt.Valid {
				t.Errorf("card-2 is still visible after the confirmed removal")
			}
		case "card-1":
			if card.DeletedAt.Valid {
				t.Errorf("card-1 was soft-deleted by an update that kept its template")
			}
		}
	}
	var reviews int64
	if err := env.db.Model(&store.Review{}).Where("card_id = ?", removed.ID).Count(&reviews).Error; err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if reviews != 1 {
		t.Errorf("reviews on the removed card = %d, want 1 (removal must keep the log)", reviews)
	}
	var states int64
	if err := env.db.Model(&store.CardState{}).Where("card_id = ?", removed.ID).Count(&states).Error; err != nil {
		t.Fatalf("count card states: %v", err)
	}
	if states != 1 {
		t.Errorf("card_states on the removed card = %d, want 1 (removal must keep progress)", states)
	}

	// One audit row records the counts of the removal.
	var audit store.AuditLog
	if err := env.db.Where("action = ?", store.ActionNoteTypeUpdate).Order("id DESC").First(&audit).Error; err != nil {
		t.Fatalf("load audit row: %v", err)
	}
	if audit.DetailJSON == nil || !strings.Contains(*audit.DetailJSON, `"cards_removed":1`) ||
		!strings.Contains(*audit.DetailJSON, `"reviews_on_removed_cards":1`) {
		t.Errorf("audit detail = %v, want the removal counts", audit.DetailJSON)
	}
}

// TestNoteTypeUpdateKindChangeRejected: the kind is fixed at creation, so switching normal to cloze
// is a 400 invalid_request that writes nothing.
func TestNoteTypeUpdateKindChangeRejected(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	owner := seedUser(t, env.db, "note_type_kind", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead, store.ScopeWrite}, nil).Plaintext
	base := fmt.Sprintf("/api/v1/decks/%s/note-types", deck.PublicID)
	nt := createNoteTypeREST(t, router, base, key, "Kind", testNoteTypeConfig())
	ntPK := noteTypePK(t, env, nt.ID)

	cloze := testNoteTypeConfig()
	cloze.Kind = notetype.KindCloze
	cloze.Templates = cloze.Templates[:1]
	code, raw := noteTypeUpdateREST(t, router, base+"/"+nt.ID, key, "Kind", cloze, 0, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("PATCH changing kind = %d, want 400 (body %s)", code, raw)
	}
	assertErrorCode(t, raw, CodeInvalidRequest)

	kept, err := store.NewNoteTypeStore(env.db).ByID(t.Context(), ntPK)
	if err != nil {
		t.Fatalf("load note type after kind change: %v", err)
	}
	if kept.Version != 0 {
		t.Errorf("version after rejected kind change = %d, want 0", kept.Version)
	}
	keptCfg, err := kept.Config()
	if err != nil {
		t.Fatalf("decode kept config: %v", err)
	}
	if keptCfg.Kind != notetype.KindNormal {
		t.Errorf("stored kind after rejected change = %q, want %q", keptCfg.Kind, notetype.KindNormal)
	}
}

// TestNoteTypeUpdateStaleVersion: the card regeneration must not bypass the optimistic lock, so a
// stale version still returns 409 version_conflict and writes no card.
func TestNoteTypeUpdateStaleVersion(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	owner := seedUser(t, env.db, "note_type_stale_cards", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead, store.ScopeWrite}, nil).Plaintext
	base := fmt.Sprintf("/api/v1/decks/%s/note-types", deck.PublicID)
	nt := createNoteTypeREST(t, router, base, key, "Stale", testNoteTypeConfig())
	ntPK := noteTypePK(t, env, nt.ID)
	onePath := base + "/" + nt.ID
	createNotesREST(t, router, deck.PublicID, nt.ID, key, []map[string]string{{"Front": "Q1", "Back": "A1"}})

	cfg := testNoteTypeConfig()
	cfg.Templates = append(cfg.Templates, notetype.Template{
		ID: "card-2", Name: "Card 2", QFormat: "{{Front}}", AFormat: "{{Back}}",
	})
	// First write wins and moves the version to 1.
	code, raw := noteTypeUpdateREST(t, router, onePath, key, "Stale", cfg, 0, nil)
	if code != http.StatusOK {
		t.Fatalf("first update = %d, want 200 (body %s)", code, raw)
	}
	// The same version again is stale: rejected before any card is regenerated.
	code, raw = noteTypeUpdateREST(t, router, onePath, key, "Stale", cfg, 0, nil)
	if code != http.StatusConflict {
		t.Fatalf("stale update = %d, want 409 (body %s)", code, raw)
	}
	assertErrorCode(t, raw, CodeVersionConflict)
	if got := len(noteTypeCards(t, env, ntPK, false)); got != 2 {
		t.Errorf("cards after the stale update = %d, want the 2 from the first update", got)
	}
}
