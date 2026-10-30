package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/store"
)

// doJSON sends a JSON request with a bearer key and returns the status code and response body.
func doJSON(t *testing.T, h http.Handler, method, path, plaintext, body string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if plaintext != "" {
		req.Header.Set("Authorization", "Bearer "+plaintext)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

// TestBulkImportIsIdempotentByExternalRef is the core acceptance:
// resubmitting the same data produces no duplicate cards, and created/updated/skipped are counted correctly.
func TestBulkImportIsIdempotentByExternalRef(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "importer", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite, store.ScopeRead}, nil)
	router := env.router()
	path := fmt.Sprintf("/api/v1/decks/%s/notes", deck.PublicID)

	body := `{"notes":[` +
		`{"kind":"basic","fields":{"front":"a","back":"b"},"external_ref":"n:1"},` +
		`{"kind":"basic","fields":{"front":"c","back":"d"},"external_ref":"n:2"}]}`

	// First import: everything created.
	status, raw := doJSON(t, router, http.MethodPost, path, k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("first import status = %d, want 200 (body %s)", status, raw)
	}
	var first importResponse
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatalf("first import response is not JSON: %v (%s)", err, raw)
	}
	if first.Created != 2 || first.Updated != 0 || first.Skipped != 0 {
		t.Errorf("first import = created %d updated %d skipped %d, want 2/0/0",
			first.Created, first.Updated, first.Skipped)
	}
	if len(first.Errors) != 0 {
		t.Errorf("first import errors = %v, want none", first.Errors)
	}

	// Second import of the same batch: everything hits an existing card by external_ref, all updated, nothing created.
	status, raw = doJSON(t, router, http.MethodPost, path, k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("second import status = %d, want 200 (body %s)", status, raw)
	}
	var second importResponse
	if err := json.Unmarshal(raw, &second); err != nil {
		t.Fatalf("second import response is not JSON: %v (%s)", err, raw)
	}
	if second.Created != 0 || second.Updated != 2 || second.Skipped != 0 {
		t.Errorf("second import = created %d updated %d skipped %d, want 0/2/0",
			second.Created, second.Updated, second.Skipped)
	}

	// Third run with on_conflict=skip: everything skipped, still nothing created.
	status, raw = doJSON(t, router, http.MethodPost, path, k.Plaintext,
		`{"on_conflict":"skip","notes":[`+
			`{"kind":"basic","fields":{"front":"a","back":"b"},"external_ref":"n:1"},`+
			`{"kind":"basic","fields":{"front":"c","back":"d"},"external_ref":"n:2"}]}`)
	if status != http.StatusOK {
		t.Fatalf("skip import status = %d, want 200 (body %s)", status, raw)
	}
	var third importResponse
	if err := json.Unmarshal(raw, &third); err != nil {
		t.Fatalf("skip import response is not JSON: %v (%s)", err, raw)
	}
	if third.Created != 0 || third.Updated != 0 || third.Skipped != 2 {
		t.Errorf("skip import = created %d updated %d skipped %d, want 0/0/2",
			third.Created, third.Updated, third.Skipped)
	}

	// The note / card counts in the database must be 2 - the repeated import produced no duplicate cards.
	assertNoteCardCount(t, env, deck.ID, 2, 2)

	// dry_run counts without writing: a new ref reports created=1 while the database still holds 2.
	status, raw = doJSON(t, router, http.MethodPost, path, k.Plaintext,
		`{"dry_run":true,"notes":[{"kind":"basic","fields":{"front":"e","back":"f"},"external_ref":"n:3"}]}`)
	if status != http.StatusOK {
		t.Fatalf("dry_run status = %d, want 200 (body %s)", status, raw)
	}
	var dry importResponse
	if err := json.Unmarshal(raw, &dry); err != nil {
		t.Fatalf("dry_run response is not JSON: %v (%s)", err, raw)
	}
	if dry.Created != 1 || !dry.DryRun {
		t.Errorf("dry_run = created %d dry_run %v, want 1/true", dry.Created, dry.DryRun)
	}
	assertNoteCardCount(t, env, deck.ID, 2, 2)
}

// assertNoteCardCount asserts the number of notes and cards under the deck.
func assertNoteCardCount(t *testing.T, env *testEnv, deckID uint64, wantNotes, wantCards int64) {
	t.Helper()
	var notes int64
	if err := env.db.Model(&store.Note{}).Where("deck_id = ?", deckID).Count(&notes).Error; err != nil {
		t.Fatalf("count notes: %v", err)
	}
	if notes != wantNotes {
		t.Errorf("notes = %d, want %d", notes, wantNotes)
	}
	var cards int64
	if err := env.db.Model(&store.Card{}).
		Joins("JOIN notes ON notes.id = cards.note_id").
		Where("notes.deck_id = ?", deckID).Count(&cards).Error; err != nil {
		t.Fatalf("count cards: %v", err)
	}
	if cards != wantCards {
		t.Errorf("cards = %d, want %d", cards, wantCards)
	}
}

// TestUpdateNoteKindAliasKeepsNoteType is the M17-37 acceptance: one deck can hold two note types of the basic shape
// (the second a copy with its own CSS), and a save that names only the transitional kind must leave the note on the type
// it already has. Resolving the alias to "the first type of that shape" used to move the note into the other type.
// A request that names a note type explicitly still follows it.
func TestUpdateNoteKindAliasKeepsNoteType(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	owner := seedUser(t, env.db, "kind_alias_owner", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead, store.ScopeWrite}, nil).Plaintext
	typesBase := fmt.Sprintf("/api/v1/decks/%s/note-types", deck.PublicID)

	stock, ok := notetype.EngramStockByKind("basic")
	if !ok {
		t.Fatal("the basic stock kind is not registered")
	}
	// Both types have the stock shape, so the kind alias cannot tell them apart; the copy differs only in CSS.
	copyConfig := stock.Config
	copyConfig.CSS = ".card { color: red; }"
	first := createNoteTypeREST(t, router, typesBase, key, "Basic", stock.Config)
	copied := createNoteTypeREST(t, router, typesBase, key, "Basic copy", copyConfig)

	createNotesREST(t, router, deck.PublicID, copied.ID, key, []map[string]string{{"front": "q1", "back": "a1"}})
	note := noteOfDeck(t, env, deck.ID)
	if got := storedNoteTypeID(t, env, note.ID); got != copied.ID {
		t.Fatalf("seeded note type = %q, want the copy %q", got, copied.ID)
	}

	// The alias must not move the note to the first type of the shape.
	code, raw := doJSON(t, router, http.MethodPatch, "/api/v1/notes/"+note.PublicID, key,
		`{"kind":"basic","fields":{"front":"q2","back":"a2"}}`)
	if code != http.StatusOK {
		t.Fatalf("PATCH with the kind alias = %d, want 200 (body %s)", code, raw)
	}
	if got := storedNoteTypeID(t, env, note.ID); got != copied.ID {
		t.Errorf("note type after a kind-alias save = %q, want the unchanged %q", got, copied.ID)
	}

	// An explicit note type is still followed.
	code, raw = doJSON(t, router, http.MethodPatch, "/api/v1/notes/"+note.PublicID, key,
		fmt.Sprintf(`{"note_type":%q,"fields":{"front":"q3","back":"a3"}}`, first.ID))
	if code != http.StatusOK {
		t.Fatalf("PATCH with an explicit note type = %d, want 200 (body %s)", code, raw)
	}
	if got := storedNoteTypeID(t, env, note.ID); got != first.ID {
		t.Errorf("note type after an explicit save = %q, want %q", got, first.ID)
	}
}

// TestImportRejectsScopeAndMalformedBody covers the negative cases for write scope and body validation.
func TestImportRejectsScopeAndMalformedBody(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "scope_user", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	readKey := seedKey(t, env.keys, user.ID, []string{store.ScopeRead}, nil)
	writeKey := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	path := fmt.Sprintf("/api/v1/decks/%s/notes", deck.PublicID)

	// A key with read scope only calling a write endpoint -> 403 scope_required.
	status, raw := doJSON(t, router, http.MethodPost, path, readKey.Plaintext,
		`{"notes":[{"kind":"basic","fields":{"front":"a","back":"b"}}]}`)
	if status != http.StatusForbidden {
		t.Fatalf("read key write status = %d, want 403 (body %s)", status, raw)
	}

	// An empty notes array -> 400 invalid_request.
	status, raw = doJSON(t, router, http.MethodPost, path, writeKey.Plaintext, `{"notes":[]}`)
	if status != http.StatusBadRequest {
		t.Fatalf("empty notes status = %d, want 400 (body %s)", status, raw)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || body.Error.Code != CodeInvalidRequest {
		t.Fatalf("error envelope = %s, want code %q", raw, CodeInvalidRequest)
	}
}
