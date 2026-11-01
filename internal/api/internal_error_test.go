package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestStoreFailureIsAnsweredAsInternalError covers a write that fails inside the database for a reason the
// service has no named mapping for. It must reach the caller as 500 internal_error with the code's static
// text: a storage failure is not a client error, and the driver's text (a constraint name, a statement, a
// path) must not be echoed in the response body.
func TestStoreFailureIsAnsweredAsInternalError(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	owner := seedUser(t, env.db, "internal_error_owner", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead, store.ScopeWrite, store.ScopeKeys}, nil).Plaintext

	typesBase := fmt.Sprintf("/api/v1/decks/%s/note-types", deck.PublicID)
	nt := createNoteTypeREST(t, router, typesBase, key, "Probe", testNoteTypeConfig())
	notesPath := fmt.Sprintf("/api/v1/decks/%s/notes", deck.PublicID)
	createBody := fmt.Sprintf(`{"notes":[{"note_type":%q,"fields":{"Front":"Q","Back":"A"},"external_ref":"probe:1"}]}`, nt.ID)
	if code, raw := doJSON(t, router, http.MethodPost, notesPath, key, createBody); code != http.StatusOK {
		t.Fatalf("POST note = %d, want 200 (body %s)", code, raw)
	}
	var note store.Note
	if err := env.db.Where("external_ref = ?", "probe:1").First(&note).Error; err != nil {
		t.Fatalf("load seeded note: %v", err)
	}

	const marker = "probe_internal_marker (1811)"
	failWrites := func(table, action string) {
		t.Helper()
		stmt := fmt.Sprintf(`CREATE TRIGGER probe_fail_%s BEFORE %s ON %s BEGIN SELECT RAISE(ABORT, '%s'); END`, table, action, table, marker)
		if err := env.db.Exec(stmt).Error; err != nil {
			t.Fatalf("create %s %s trigger: %v", table, action, err)
		}
	}
	assertInternal := func(name string, code int, raw []byte) {
		t.Helper()
		if code != http.StatusInternalServerError {
			t.Fatalf("%s = %d %s, want 500", name, code, raw)
		}
		assertErrorCode(t, raw, CodeInternal)
		for _, leak := range []string{marker, "constraint failed", "SQL"} {
			if strings.Contains(string(raw), leak) {
				t.Fatalf("%s body leaks the store error (%q): %s", name, leak, raw)
			}
		}
	}

	t.Run("note update", func(t *testing.T) {
		failWrites("notes", "UPDATE")
		updateBody := fmt.Sprintf(`{"note_type":%q,"fields":{"Front":"Q2","Back":"A2"}}`, nt.ID)
		code, raw := doJSON(t, router, http.MethodPatch, "/api/v1/notes/"+note.PublicID, key, updateBody)
		assertInternal("PATCH note", code, raw)
	})

	t.Run("note type write", func(t *testing.T) {
		failWrites("note_types", "UPDATE")
		code, raw := doJSON(t, router, http.MethodPatch, typesBase+"/"+nt.ID, key, noteTypeBody("Renamed", testNoteTypeConfig(), store.Ptr(0)))
		assertInternal("PATCH note type", code, raw)
	})

	t.Run("key creation", func(t *testing.T) {
		failWrites("api_keys", "INSERT")
		code, raw := doJSON(t, router, http.MethodPost, "/api/v1/keys", key, `{"name":"probe","scopes":["read"]}`)
		assertInternal("POST key", code, raw)
	})

	// The writes really were attempted: a named store refusal still maps as before, which shows the trigger
	// did not turn every write into a 500.
	if err := env.db.Exec("DROP TRIGGER probe_fail_api_keys").Error; err != nil {
		t.Fatalf("drop api_keys trigger: %v", err)
	}
	if code, raw := doJSON(t, router, http.MethodPost, "/api/v1/keys", key, `{"name":"probe","scopes":[]}`); code != http.StatusCreated {
		t.Fatalf("POST key without the trigger = %d %s, want 201", code, raw)
	}
}
