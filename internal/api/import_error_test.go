package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestBulkImportRowFailureDoesNotEchoDriverText covers the per-row failure report of POST /decks/:id/notes.
//
// An aborting BEFORE INSERT trigger makes SQLite return a message containing the trigger name and its "constraint
// failed" prefix. That text is a server-side diagnostic and must reach the log only: the 200 report carries the
// stable internal_error code instead, so a caller cannot tell which driver ran or read SQL text out of a failure.
func TestBulkImportRowFailureDoesNotEchoDriverText(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "import_error", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	noteType := createStockNoteType(t, env.db, deck.ID, "basic")

	if err := env.db.Exec(`CREATE TRIGGER probe_internal_marker BEFORE INSERT ON notes
		BEGIN SELECT RAISE(ABORT, 'probe_internal_marker'); END;`).Error; err != nil {
		t.Fatalf("create aborting trigger: %v", err)
	}

	body := fmt.Sprintf(`{"notes":[{"note_type":%q,"fields":{"front":"q","back":"a"}}]}`, noteType.PublicID)
	status, raw := doJSON(t, env.router(), http.MethodPost, fmt.Sprintf("/api/v1/decks/%s/notes", deck.PublicID), k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("bulk import status = %d, want 200 with a per-row error (body %s)", status, raw)
	}
	var resp ImportResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode import response: %v (%s)", err, raw)
	}
	if len(resp.Errors) != 1 {
		t.Fatalf("errors = %+v, want exactly one row error", resp.Errors)
	}
	reason := resp.Errors[0].Reason
	if reason != CodeInternal {
		t.Errorf("row error reason = %q, want the stable %q", reason, CodeInternal)
	}
	for _, leak := range []string{"probe_internal_marker", "constraint failed", "SQL"} {
		if strings.Contains(reason, leak) {
			t.Errorf("row error reason = %q, want no driver text %q", reason, leak)
		}
	}

	var notes int64
	if err := env.db.Model(&store.Note{}).Where("deck_id = ?", deck.ID).Count(&notes).Error; err != nil {
		t.Fatalf("count notes: %v", err)
	}
	if notes != 0 {
		t.Errorf("import left %d notes, want the failed row rolled back", notes)
	}
}
